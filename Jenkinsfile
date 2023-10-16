pipeline {
  agent {
    label 'node-arm64'
  }
  environment {
    ECR_REGISTRY = "036703920845.dkr.ecr.us-east-1.amazonaws.com"
      DOCKER_IMAGE = "karpenter-node-holder"
      TAG = "latest"
  }

  stages {
    stage('Build Image') {
      steps {
        script {
          // Build the Docker image
          sh "docker build -t $DOCKER_IMAGE:$TAG ."
        }
      }
    }

    stage('Push Image') {
      when {
        branch 'main'
      }
      steps {
        script {
          withCredentials([usernamePassword(credentialsId: 'your-docker-hub-credentials', usernameVariable: 'DOCKER_USERNAME', passwordVariable: 'DOCKER_PASSWORD')]) {
            sh "docker login -u $DOCKER_USERNAME -p $DOCKER_PASSWORD"
          }

          // Push the Docker image to the registry
          sh "docker push $ECR_REGISTRY/$DOCKER_IMAGE:$TAG"
        }
      }
    }

    stage('Deploy with ArgoCD') {
      when {
        branch 'main'
      }
      steps {
        script {
          // Deploy the Docker image to the registry
          sh "argocd app sync karpenter-node-holder"
        }
      }
    }

  }
}
