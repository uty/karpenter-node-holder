package main

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	config, err := rest.InClusterConfig()
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime)

	logger.Printf("Got incluster config\n")
	if err != nil {
		logger.Printf("Error creating in-cluster config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	logger.Printf("Got clientset")
	if err != nil {
		logger.Printf("Error creating Kubernetes clientset: %v\n", err)
		os.Exit(1)
	}

	listWatcher := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return clientset.CoreV1().Nodes().List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return clientset.CoreV1().Nodes().Watch(context.TODO(), options)
		},
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
				logger.Printf("Node added: %s\n", node.Name)

				// Stop the timer and then add annotation and start a new timer
				pauseConsolidation(clientset, listNodes(clientset, logger), &mu, &timer, &timerMutex, logger)
			},
			// DeleteFunc: func(obj interface{}) {
			// },
		},
	)
	// Start the informer to begin watching for changes
	stopCh := make(chan struct{})
	defer close(stopCh)

	go informer.Run(stopCh)

	// Wait for the informer to sync
	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		logger.Println("Timed out waiting for caches to sync")
		os.Exit(1)
	}

	// Use a WaitGroup to keep the program running
	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}

// Get the hold duration from the environment variable
// Parameters:
//
//	logger: Logger
//
// Returns: Hold duration
func getHoldDuration(logger *log.Logger) time.Duration {
	holdDurationStr := os.Getenv("HOLD_DURATION")
	holdDuration, err := strconv.Atoi(holdDurationStr)
	if err != nil {
		logger.Printf("Error parsing hold duration: %v\n", err)
		os.Exit(1)
	}
	return time.Duration(holdDuration)
}

// List all nodes
// Parameters:
//
//	clientset: Kubernetes clientset
//	logger: Logger
//
//	Returns: List of nodes
func listNodes(clientset *kubernetes.Clientset, logger *log.Logger) *corev1.NodeList {
	nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		logger.Printf("Error listing nodes: %v\n", err)
		os.Exit(1)
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
		if node.Annotations == nil {
			node.Annotations = make(map[string]string)
		}
		node.Annotations[holdAnnotation] = "true"
		retryDelay := 5 * time.Second
		retryCount := 0
		maxRetries := 5
		for {
			_, err := clientset.CoreV1().Nodes().Update(context.TODO(), &node, metav1.UpdateOptions{})
			if err == nil {
				logger.Printf("Successfully annotated node %s\n", node.Name)
				break
			} else {
				logger.Printf("Error annotating node %s: %v\n", node.Name, err)
				if retryCount >= maxRetries {
					logger.Printf("Max retries reached, giving up on node %s\n", node.Name)
					break
				}
				updatedNode, err := clientset.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
				if err != nil {
					logger.Printf("Error updating node info %s: %v\n", node.Name, err)
					break
				}
				node = *updatedNode
				if node.Annotations == nil {
					node.Annotations = make(map[string]string)
				}
				node.Annotations[holdAnnotation] = "true"
				time.Sleep(retryDelay)
				retryCount++
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
		if node.Annotations == nil {
			logger.Printf("Node %s has no annotations, skipping\n", node.Name)
			continue
		}
		delete(node.Annotations, holdAnnotation)
		retryDelay := 5 * time.Second
		retryCount := 0
		maxRetries := 5
		for {
			_, err := clientset.CoreV1().Nodes().Update(context.TODO(), &node, metav1.UpdateOptions{})
			if err == nil {
				logger.Printf("Successfully removed annotation from node %s\n", node.Name)
				break
			} else {
				logger.Printf("Error removing annotation from node %s: %v\n", node.Name, err)
				if retryCount >= maxRetries {
					logger.Printf("Max retries reached, giving up on node %s\n", node.Name)
					break
				}
				updatedNode, err := clientset.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
				if err != nil {
					logger.Printf("Error updating node info %s: %v\n", node.Name, err)
					break
				}
				node = *updatedNode
				if node.Annotations == nil {
					continue
				}
				delete(node.Annotations, holdAnnotation)
				time.Sleep(retryDelay)
				retryCount++
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
//	nodeList: List of nodes
//	mu: Mutex to lock before updating the timer
//	timer: Pointer to the timer
//	timerMutex: Mutex to lock before updating the timer
//	logger: Logger
func pauseConsolidation(clientset *kubernetes.Clientset, nodeList *corev1.NodeList, mu *sync.Mutex, timer **time.Timer, timerMutex *sync.Mutex, logger *log.Logger) {
	holdAnnotation := os.Getenv("HOLD_ANNOTATION")
	mu.Lock()
	defer mu.Unlock()

	timerMutex.Lock()
	if *timer != nil && (*timer).Stop() {
		// Drain the timer's channel if it's still active
		select {
		case <-(*timer).C:
		default:
		}
		logger.Printf("Stopping previous timer\n")
	} else {
		// Annotate all nodes with "karpenter.sh/do-not-consolidate=true"
		logger.Printf("Adding annotation %s to all nodes\n", holdAnnotation)
		annotateNodes(clientset, nodeList, holdAnnotation, logger)
	}

	// Start the timer to remove the annotation after holdDuration minutes
	logger.Printf("Starting timer to remove annotation in %d minutes\n", getHoldDuration(logger))
	*timer = time.AfterFunc(getHoldDuration(logger)*time.Minute, func() {
		mu.Lock()
		defer mu.Unlock()

		logger.Println("Removing annotation from all nodes")

		// Remove the annotation from all nodes
		removeAnnotationFromNodes(clientset, nodeList, holdAnnotation, logger)
	})
	timerMutex.Unlock()
}
