package main

import (
  "log"
  "os"
  "time"
  "strconv"
  "context"
  "sync"
  "k8s.io/client-go/tools/cache"
  corev1 "k8s.io/api/core/v1"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

  "k8s.io/apimachinery/pkg/runtime"
  "k8s.io/apimachinery/pkg/watch"
  "k8s.io/client-go/kubernetes"
  "k8s.io/client-go/rest"
)

func main() {
  config, err := rest.InClusterConfig()
  l := log.New(os.Stdout, "", log.Ldate | log.Ltime)

  l.Printf("Got incluster config\n")
  if err != nil {
    l.Printf("Error creating in-cluster config: %v\n", err)
    os.Exit(1)
  }

  clientset, err := kubernetes.NewForConfig(config)
  l.Printf("Got clientset")
  if err != nil {
    l.Printf("Error creating Kubernetes clientset: %v\n", err)
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
    &corev1.Node{}, // Type of resource to watch
    time.Duration(0), // No resync
    cache.ResourceEventHandlerFuncs{
      AddFunc: func(obj interface{}) {
        node := obj.(*corev1.Node)
        l.Printf("Node added: %s\n", node.Name)

        // Stop the timer and then add annotation and start a new timer
        pauseConsolidation(clientset, listNodes(clientset, l), &mu, &timer, &timerMutex, l)
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
    l.Println("Timed out waiting for caches to sync")
    os.Exit(1)
  }

  // Use a WaitGroup to keep the program running
  var wg sync.WaitGroup
  wg.Add(1)
  wg.Wait()
}

func getHoldDuration(l *log.Logger) time.Duration {
  holdDurationStr := os.Getenv("HOLD_DURATION")
  holdDuration, err := strconv.Atoi(holdDurationStr)
  if err != nil {
    l.Printf("Error parsing hold duration: %v\n", err)
    os.Exit(1)
  }
  return time.Duration(holdDuration)
}

func listNodes(clientset *kubernetes.Clientset, l *log.Logger) (*corev1.NodeList) {
  nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{
  })
  if err != nil {
    l.Printf("Error listing nodes: %v\n", err)
    os.Exit(1)
  }
  return nodeList
}

func annotateNodes(clientset *kubernetes.Clientset, nodeList *corev1.NodeList, holdAnnotation string, l *log.Logger) {
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
        l.Printf("Successfully annotated node %s\n", node.Name)
        break
      } else {
        l.Printf("Error annotating node %s: %v\n", node.Name, err)
        if retryCount >= maxRetries {
          l.Printf("Max retries reached, giving up on node %s\n", node.Name)
          break
        }
        updatedNode, err := clientset.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
        if err != nil {
          l.Printf("Error updating node info %s: %v\n", node.Name, err)
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

func removeAnnotationFromNodes(clientset *kubernetes.Clientset, nodeList *corev1.NodeList, holdAnnotation string, l *log.Logger) {
  for _, node := range nodeList.Items {
    if node.Annotations == nil {
      l.Printf("Node %s has no annotations, skipping\n", node.Name)
      continue
    }
    delete(node.Annotations, holdAnnotation)
    retryDelay := 5 * time.Second
    retryCount := 0
    maxRetries := 5
    for {
      _, err := clientset.CoreV1().Nodes().Update(context.TODO(), &node, metav1.UpdateOptions{})
      if err == nil {
        l.Printf("Successfully removed annotation from node %s\n", node.Name)
        break
      } else {
        l.Printf("Error removing annotation from node %s: %v\n", node.Name, err)
        if retryCount >= maxRetries {
          l.Printf("Max retries reached, giving up on node %s\n", node.Name)
          break
        }
        updatedNode, err := clientset.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
        if err != nil {
          l.Printf("Error updating node info %s: %v\n", node.Name, err)
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

func pauseConsolidation(clientset *kubernetes.Clientset, nodeList *corev1.NodeList, mu *sync.Mutex, timer **time.Timer, timerMutex *sync.Mutex, l *log.Logger) {
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
    l.Printf("Stopping previous timer\n")
  } else {
    // Annotate all nodes with "karpenter.sh/do-not-consolidate=true"
    l.Printf("Adding annotation %s to all nodes\n", holdAnnotation)
    annotateNodes(clientset, nodeList, holdAnnotation, l)
  }

  // Start the timer to remove the annotation after holdDuration minutes
  l.Printf("Starting timer to remove annotation in %d minutes\n", getHoldDuration(l))
  *timer = time.AfterFunc(getHoldDuration(l)*time.Minute, func() {
    mu.Lock()
    defer mu.Unlock()

    l.Println("Removing annotation from all nodes")

    // Remove the annotation from all nodes
    removeAnnotationFromNodes(clientset, nodeList, holdAnnotation, l)
  })
  timerMutex.Unlock()
}
