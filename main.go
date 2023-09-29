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

  err := removeLabelFromPod()
  if err != nil {
    fmt.Printf("Error removing label: %v\n", err)
    os.Exit(1)
  }

  fmt.Println("Label removed. Sleeping...")
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

func removeLabelFromPod() error {
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
  holdLabel := os.Getenv("HOLD_LABEL")
  fmt.Printf("Removing label %s from pod %s in namespace %s\n", holdLabel, podName, namespace)

  retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
    pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
    if err != nil {
      return err
    }
    fmt.Printf("Got pod %s\n", pod.Name)

    // Remove the label from pod labels
    delete(pod.Labels, holdLabel)
    fmt.Printf("Removed label %s from pod %s\n", holdLabel, pod.Name)

    _, err = clientset.CoreV1().Pods(namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
    return err
  })

  return retryErr
}
