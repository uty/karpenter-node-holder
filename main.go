package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	// Initial delay before annotating nodes
	INITIAL_DELAY = 30 * time.Second
	// Maximum number of retries before giving up on annotating or removing an annotation from a node
	MAX_RETRIES = 5
	// Delay between retries
	RETRY_DELAY = 5 * time.Second
)

func main() {
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime)
	config, err := rest.InClusterConfig()
	if err != nil {
		logger.Fatalf("Error creating in-cluster config: %v\n", err)
	}
	logger.Println("Got incluster config")

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Fatalf("Error creating Kubernetes clientset: %v\n", err)
	}
	logger.Println("Got clientset")

	listWatcher := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return clientset.CoreV1().Nodes().List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return clientset.CoreV1().Nodes().Watch(context.TODO(), options)
		},
	}

	// Record the UIDs of all present nodes so we don't process them when we just started
	var recordedUIDs = make(map[string]bool)
	for _, node := range listNodes(clientset, logger).Items {
		recordedUIDs[string(node.UID)] = true
	}

	var mu sync.Mutex
	var timer *time.Timer
	var timerMutex sync.Mutex

	_, informer := cache.NewInformer(
		listWatcher,
		&corev1.Node{},   // Type of resource to watch
		time.Duration(0), // No resync
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				node := obj.(*corev1.Node)
				// Skip nodes that were already running when we started
				if !recordedUIDs[string(node.UID)] {
					logger.Printf("Node added: %s\n", node.Name)

					time.Sleep(getInitialDelay())
					// Stop the timer and then add annotation and start a new timer
					pauseConsolidation(clientset, &mu, &timer, &timerMutex, logger)
				}
			},
			DeleteFunc: func(obj interface{}) {
				node := obj.(*corev1.Node)
				logger.Printf("Node deleted: %s\n", node.Name)
			},
		},
	)
	logger.Println("Created informer")
	// Start the informer to begin watching for changes
	stopCh := make(chan struct{})
	defer close(stopCh)

	go informer.Run(stopCh)
	logger.Println("Started informer")

	// Wait for the informer to sync
	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		logger.Fatalf("Timed out waiting for caches to sync")
	}

	// Use a WaitGroup to keep the program running
	var wg sync.WaitGroup
	wg.Add(1)
	// Signal handler to gracefully shut down
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		defer wg.Done()
		sig := <-signalCh
		logger.Printf("Received signal: %v\n", sig)

		// Stop the informer by closing the stop channel
		close(stopCh)

		// Remove the annotation from all nodes before exiting
		mu.Lock()
		defer mu.Unlock()
		holdAnnotation := os.Getenv("HOLD_ANNOTATION")
		removeAnnotationFromNodes(clientset, listNodes(clientset, logger), holdAnnotation, logger)
		logger.Println("Removed annotation from all nodes before exiting")
	}()
	wg.Wait()
}

// Get the hold duration from the environment variable. This is the duration of how long to pause consolidation
// Parameters:
//
//	logger: Logger
//
// Returns: Hold duration
func getHoldDuration(logger *log.Logger) time.Duration {
	holdDurationStr := os.Getenv("HOLD_DURATION")
	holdDuration, err := strconv.Atoi(holdDurationStr)
	if err != nil {
		logger.Fatalf("Error parsing hold duration: %v\n", err)
	}
	return time.Duration(holdDuration) * time.Minute
}

// Get the initial delay from the environment variable. This is the duration of how long to wait before pausing consolidation
// Returns: Initial delay duration
func getInitialDelay() time.Duration {
	initialDelayStr := os.Getenv("INITIAL_DELAY")
	initialDelay, err := strconv.Atoi(initialDelayStr)
	if err != nil {
		return INITIAL_DELAY
	}
	return time.Duration(initialDelay) * time.Second
}

// List all nodes
// Parameters:
//
//	clientset: Kubernetes clientset
//	logger: Logger
//
// Returns: List of nodes
func listNodes(clientset *kubernetes.Clientset, logger *log.Logger) *corev1.NodeList {
	nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		logger.Fatalf("Error listing nodes: %v\n", err)
	}
	return nodeList
}

// Annotate all nodes with the given annotation
//
// Parameters:
//
//	clientset: Kubernetes clientset
//	nodeList: List of nodes
//	holdAnnotation: Annotation to add
//	logger: Logger
func annotateNodes(clientset *kubernetes.Clientset, nodeList *corev1.NodeList, holdAnnotation string, logger *log.Logger) {
	for _, node := range nodeList.Items {
		retryCount := 0
		for {
			if node.Annotations == nil {
				node.Annotations = make(map[string]string)
			}
			if node.Annotations[holdAnnotation] == "true" {
				logger.Printf("Node %s already annotated, skipping\n", node.Name)
				break
			}
			node.Annotations[holdAnnotation] = "true"

			_, err := clientset.CoreV1().Nodes().Update(context.TODO(), &node, metav1.UpdateOptions{})
			if err == nil {
				logger.Printf("Successfully annotated node %s\n", node.Name)
				break
			} else {
				logger.Printf("Error annotating node %s: %v\n", node.Name, err)
				if retryCount >= MAX_RETRIES {
					logger.Printf("Max retries reached, giving up on node %s\n", node.Name)
					break
				}

				time.Sleep(RETRY_DELAY)
				retryCount++

				updatedNode, err := clientset.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
				if err != nil {
					logger.Printf("Error updating node info %s: %v\n", node.Name, err)
					break
				}
				node = *updatedNode
			}
		}
	}
}

// Remove the annotation from all nodes
//
// Parameters:
//
//	clientset: Kubernetes clientset
//	nodeList: List of nodes
//	holdAnnotation: Annotation to remove
//	logger: Logger
func removeAnnotationFromNodes(clientset *kubernetes.Clientset, nodeList *corev1.NodeList, holdAnnotation string, logger *log.Logger) {
	for _, node := range nodeList.Items {
		retryCount := 0
		for {
			if node.Annotations == nil || node.Annotations[holdAnnotation] != "true" {
				logger.Printf("Node %s doesn't have the annotation, skipping\n", node.Name)
				break
			}
			delete(node.Annotations, holdAnnotation)
			_, err := clientset.CoreV1().Nodes().Update(context.TODO(), &node, metav1.UpdateOptions{})
			if err == nil {
				logger.Printf("Successfully removed annotation from node %s\n", node.Name)
				break
			} else {
				logger.Printf("Error removing annotation from node %s: %v\n", node.Name, err)
				if retryCount >= MAX_RETRIES {
					logger.Printf("Max retries reached, giving up on node %s\n", node.Name)
					break
				}

				time.Sleep(RETRY_DELAY)
				retryCount++

				updatedNode, err := clientset.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
				if err != nil {
					logger.Printf("Error updating node info %s: %v\n", node.Name, err)
					break
				}
				node = *updatedNode
			}
		}
	}
}

// Pause consolidation by adding an annotation to all nodes and starting a timer
// to remove the annotation after holdDuration minutes
//
// Parameters:
//
//	clientset: Kubernetes clientset
//	mu: Mutex to lock before updating the timer
//	timer: Pointer to the timer
//	timerMutex: Mutex to lock before updating the timer
//	logger: Logger
func pauseConsolidation(clientset *kubernetes.Clientset, mu *sync.Mutex, timer **time.Timer, timerMutex *sync.Mutex, logger *log.Logger) {
	holdAnnotation := os.Getenv("HOLD_ANNOTATION")
	mu.Lock()
	defer mu.Unlock()
	timerMutex.Lock()
	defer timerMutex.Unlock()

	if *timer != nil && (*timer).Stop() {
		// Drain the timer's channel if it's still active
		select {
		case <-(*timer).C:
		default:
		}
		logger.Printf("Stopping previous timer\n")
	}
	// Annotate all nodes with "karpenter.sh/do-not-consolidate=true"
	logger.Printf("Adding annotation %s to all nodes\n", holdAnnotation)
	annotateNodes(clientset, listNodes(clientset, logger), holdAnnotation, logger)

	// Start the timer to remove the annotation after holdDuration minutes
	logger.Printf("Starting timer to remove annotation in %v\n", getHoldDuration(logger))
	*timer = time.AfterFunc(getHoldDuration(logger), func() {
		mu.Lock()
		defer mu.Unlock()

		logger.Println("Removing annotation from all nodes")

		// Remove the annotation from all nodes
		removeAnnotationFromNodes(clientset, listNodes(clientset, logger), holdAnnotation, logger)
	})
}
