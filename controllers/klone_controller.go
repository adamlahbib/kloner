/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/admida0ui/kloner/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type DiscordNotificationObject struct {
	Username string `json:"username"`
	Content  string `json:"content"`
}

func SendDiscordWebHookMsg(notify DiscordNotificationObject) error {

	// You should make the webhook url private and not hard coding in your code.
	// i.e. store in database or setup environment
	webhookURL := "https://discord.com/api/webhooks/1129117209000685690/Vs8H5F6rCX5ETyC1jbffcydJAmp1SpKGAdDwclSM3lNpi-0vPG8KJwsyKuUoR8GkhuY1"

	jsonStringByte, err := json.Marshal(notify)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonStringByte))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		return errors.New("Send request failed")
	}
	return nil
}

func (r *KloneReconciler) CloneRepo(url string, name string) error {
	// Clone the repository into the current directory
	// _, err := git.PlainClone("./"+dirname, false, &git.CloneOptions{
	// 	URL:      url,
	// 	Progress: os.Stdout,
	// })

	// if err != nil {
	// 	return err
	// }

	podSpec := &v1.PodSpec{
		Containers: []v1.Container{
			{
				Name:  name,
				Image: "alpine/git",

				//Command: []string{"git", "clone", url},
				// make a command to clone the git repo and list the contents of the directory
				Command: []string{"sh", "-c", "git clone " + url + " && ls -l " + name},
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      "output",
						MountPath: "/output",
					},
				},
			},
		},
		Volumes: []v1.Volume{
			{
				Name: "output",
				VolumeSource: v1.VolumeSource{
					HostPath: &v1.HostPathVolumeSource{
						Path: "/output",
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: ctrl.ObjectMeta{
			Name:      name,
			Namespace: "kloner-system",
		},
		Spec: *podSpec,
	}

	_, err := ctrl.CreateOrUpdate(context.Background(), r.Client, pod, func() error {
		return nil
	})

	if err != nil {
		return err
	} else {
		fmt.Println(name, " Repository cloned successfully!")
	}

	go r.runGoProgramInPod(pod)

	return nil

}

func (r *KloneReconciler) waitForPodStatus(pod *v1.Pod, status v1.PodPhase) (bool, error) {
	for {
		// Get the latest version of the Pod
		err := r.Get(context.Background(), client.ObjectKey{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}, pod)

		if err != nil {
			return false, err
		}

		// Check if the Pod is in a ready state
		if pod.Status.Phase == status {
			fmt.Println(pod.Name, " status achieved")
			return true, nil
		}

		// Wait for one second
		time.Sleep(1 * time.Second)
	}
}

func (r *KloneReconciler) detectPodErrorAndDelete(clientset *kubernetes.Clientset, pod *v1.Pod) {

	for _, c := range pod.Status.ContainerStatuses {
		if c.State.Waiting != nil && c.State.Waiting.Reason == "CrashLoopBackOff" {
			fmt.Println("Pod is in CrashLoopBackOff state")
			break
		}
	}

	fmt.Println("Deleting pod " + pod.Name)
	err := clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})

	if err != nil {
		fmt.Println("Error deleting Pod: ", err)
	} else {
		fmt.Println(pod.Name + " Pod deleted successfully!")
	}

	// delete klone resource associated with it, it has the same name

	// fetch klone resource by name

	klone := &appsv1alpha1.Klone{}

	err = r.Get(context.Background(), client.ObjectKey{
		Namespace: "default",
		Name:      pod.Name,
	}, klone)

	if err != nil {
		fmt.Println("Error getting Klone: ", err)
	}

	// delete associated klone resource

	err = r.Delete(context.Background(), klone)

	if err != nil {
		fmt.Println("Error deleting Klone: ", err)
	} else {
		fmt.Println(klone.Name + " Klone deleted successfully!")
	}

}

func (r *KloneReconciler) retrievePodLogs(clientset *kubernetes.Clientset, pod *v1.Pod) (string, error) {
	// Get the Pod logs
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &v1.PodLogOptions{})

	// Stream the logs from the Pod
	stream, err := req.Stream(context.Background())

	if err != nil {
		return "", err
	}

	defer stream.Close()

	buf := new(bytes.Buffer)

	// Copy the logs from the Pod to the buffer
	_, err = io.Copy(buf, stream)

	if err != nil {
		return "", err
	}

	// return logs
	return buf.String(), nil
}

func (r *KloneReconciler) runGoProgramInPod(pod *v1.Pod) {

	config, err := rest.InClusterConfig() // instead of using kubeconfig file, to make it container and portable
	if err != nil {
		fmt.Println("0", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println("1", err)
	}

	// Wait for the Pod to be ready

	_, err = r.waitForPodStatus(pod, v1.PodRunning)

	if err != nil {
		fmt.Println("Error waiting for Pod to be ready: ", err)
	}

	// Retrieve the Pod logs or execute commands inside the Pod container

	logs, err := r.retrievePodLogs(clientset, pod)

	if err != nil {
		fmt.Println("Error retrieving Pod logs: ", err)
	}

	fmt.Println(logs)
	// just putting the logic of gettings logs cuz uses clientset

	// lets try status to kill the pod and delete the klone resource associated with it

	// check1, err := r.waitForPodStatus(pod, v1.PodSucceeded) using goroutines in this case!
	// check2, err := r.waitForPodStatus(pod, v1.PodFailed)

	r.detectPodErrorAndDelete(clientset, pod)

}

// KloneReconciler reconciles a Klone object
type KloneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=apps.ctf8s.com,resources=klones,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.ctf8s.com,resources=klones/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps.ctf8s.com,resources=klones/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods;pods/log,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Klone object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *KloneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO(user): your logic here
	// Fetch the Klone instance
	instance := &appsv1alpha1.Klone{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		return ctrl.Result{}, err
	}

	//clone the repo
	err = r.CloneRepo(instance.Spec.URL, instance.Spec.Name)
	if err != nil {
		fmt.Println("3", err)
	}

	// send the status to discord webhook

	// err = SendDiscordWebHookMsg(DiscordNotificationObject{
	// 	Username: "Kloner",
	// 	Content:  instance.Spec.Name + " Repository cloned successfully!",
	// })

	// if err != nil {
	// 	fmt.Println("4", err)
	// }

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KloneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.Klone{}).
		Complete(r)
}
