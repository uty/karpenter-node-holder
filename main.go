package main

import (
  "fmt"
  "os"
  "time"
  "strconv"
  "context"
  "sync"
  "k8s.io/client-go/tools/cache"
  corev1 "k8s.io/api/core/v1"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

  "k8s.io/client-go/kubernetes"
  "k8s.io/client-go/rest"
  "k8s.io/apimachinery/pkg/fields"
)

func main() {
  config, err := rest.InClusterConfig()
  fmt.Printf("Got incluster config")
  if err != nil {
    fmt.Printf("Error creating in-cluster config: %v\n", err)
    os.Exit(1)
  }

  clientset, err := kubernetes.NewForConfig(config)
  fmt.Printf("Got clientset")
  if err != nil {
    fmt.Printf("Error creating Kubernetes clientset: %v\n", err)
    os.Exit(1)
  }

  daemonSetName := os.Getenv("DAEMONSET_NAME")
  namespace := os.Getenv("NAMESPACE")

  selector, err := fields.ParseSelector("app=" + daemonSetName)
  if err != nil {
    fmt.Printf("Error parsing selector: %v\n", err)
    os.Exit(1)
  }

  listWatcher := cache.NewListWatchFromClient(
    clientset.CoreV1().RESTClient(),
    "pods",
    namespace,
    selector,
  )

  var mu sync.Mutex
  // Create a timer to remove the annotation after 10 minutes
  var timer *time.Timer
  var timerMutex sync.Mutex

  _, informer := cache.NewInformer(
    listWatcher,
    &corev1.Pod{}, // Type of resource to watch
    time.Duration(0), // No resync
    cache.ResourceEventHandlerFuncs{
      AddFunc: func(obj interface{}) {
        pod := obj.(*corev1.Pod)
        fmt.Printf("Pod added: %s\n", pod.Name)

        // Stop the timer and then add annotation and start a new timer
        addAnnotationToPods(clientset, namespace, listPods(clientset, namespace, daemonSetName), &mu, timer, &timerMutex)
      },
      // DeleteFunc: func(obj interface{}) {
      //   pod := obj.(*corev1.Pod)
      //   fmt.Printf("Pod deleted: %s\n", pod.Name)
      // },
    },
  )
  // Start the informer to begin watching for changes
  stopCh := make(chan struct{})
  defer close(stopCh)

  go informer.Run(stopCh)

  // Wait for the informer to sync
  if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
    fmt.Println("Timed out waiting for caches to sync")
    os.Exit(1)
  }

  // Use a WaitGroup to keep the program running
  var wg sync.WaitGroup
  wg.Add(1)
  wg.Wait()
}

func getHoldDuration() time.Duration {
  holdDurationStr := os.Getenv("HOLD_DURATION")
  holdDuration, err := strconv.Atoi(holdDurationStr)
  if err != nil {
    fmt.Printf("Error parsing hold duration: %v\n", err)
    os.Exit(1)
  }
  return time.Duration(holdDuration)
}

func listPods(clientset *kubernetes.Clientset, namespace string, daemonSetName string) (*corev1.PodList) {
  podList, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
    LabelSelector: "app=" + daemonSetName, // Replace with your label selector
  })
  if err != nil {
    fmt.Printf("Error listing pods: %v\n", err)
    os.Exit(1)
  }
  return podList
}


func addAnnotationToPods(clientset *kubernetes.Clientset, namespace string, podList *corev1.PodList, mu *sync.Mutex, timer *time.Timer, timerMutex *sync.Mutex) {
  holdAnnotation := os.Getenv("HOLD_ANNOTATION")
  mu.Lock()
  defer mu.Unlock()

  fmt.Println("Adding annotation to all pods")

  // Annotate all pods with "karpenter.sh/do-not-evict=true"
  for _, pod := range podList.Items {
    pod.Annotations[holdAnnotation] = "true"
    _, err := clientset.CoreV1().Pods(namespace).Update(context.TODO(), &pod, metav1.UpdateOptions{})
    if err != nil {
      fmt.Printf("Error annotating pod %s: %v\n", pod.Name, err)
    }
  }

  // Start the timer to remove the annotation after holdDuration minutes
  timerMutex.Lock()
  if timer != nil {
    timer.Stop() // Cancel the previous timer if it exists
  }
  timer = time.AfterFunc(getHoldDuration()*time.Minute, func() {
    mu.Lock()
    defer mu.Unlock()

    fmt.Println("Removing annotation from all pods")

    // Remove the annotation from all pods
    for _, pod := range podList.Items {
      delete(pod.Annotations, holdAnnotation)
      _, err := clientset.CoreV1().Pods(namespace).Update(context.TODO(), &pod, metav1.UpdateOptions{})
      if err != nil {
        fmt.Printf("Error removing annotation from pod %s: %v\n", pod.Name, err)
      }
    }
  })
  timerMutex.Unlock()
}
