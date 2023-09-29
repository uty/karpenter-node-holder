package main

import (
  "fmt"
  "os"
  "time"
  "strconv"
  "context"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

  "k8s.io/client-go/kubernetes"
  "k8s.io/client-go/rest"
  "k8s.io/client-go/util/retry"
)

func main() {
  sleepDuration := getSleepDuration()
  fmt.Printf("Sleeping for %d minutes...\n", sleepDuration)

  time.Sleep(time.Duration(sleepDuration) * time.Minute)

  err := removeAnnotationFromPod()
  if err != nil {
    fmt.Printf("Error removing annotation: %v\n", err)
    os.Exit(1)
  }

  fmt.Println("Annotation removed. Sleeping...")
  select {}
}

func getSleepDuration() int {
  sleepDurationStr := os.Getenv("SLEEP_DURATION")
  sleepDuration, err := strconv.Atoi(sleepDurationStr)
  if err != nil {
    fmt.Printf("Error parsing sleep duration: %v\n", err)
    os.Exit(1)
  }
  return sleepDuration
}

func removeAnnotationFromPod() error {
  config, err := rest.InClusterConfig()
    fmt.Printf("Got incluster config")
  if err != nil {
    return err
  }

  clientset, err := kubernetes.NewForConfig(config)
    fmt.Printf("Got clientset")
  if err != nil {
    return err
  }

  podName := os.Getenv("POD_NAME")
  namespace := os.Getenv("NAMESPACE")
  holdAnnotation := os.Getenv("HOLD_ANNOTATION")
  fmt.Printf("Removing annotation %s from pod %s in namespace %s\n", holdAnnotation, podName, namespace)

  retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
    pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
    if err != nil {
      return err
    }
    fmt.Printf("Got pod %s\n", pod.Name)

    // Remove the annotation from pod annotations
    delete(pod.Annotations, holdAnnotation)
    fmt.Printf("Removed annotation %s from pod %s\n", holdAnnotation, pod.Name)

    _, err = clientset.CoreV1().Pods(namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
    return err
  })

  return retryErr
}
