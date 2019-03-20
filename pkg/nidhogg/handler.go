package nidhogg

import (
	"context"
	"fmt"
	"log"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Handler performs the main business logic of the Wave controller
type Handler struct {
	client.Client
	recorder record.EventRecorder
}

// NewHandler constructs a new instance of Handler
func NewHandler(c client.Client, r record.EventRecorder) *Handler {
	return &Handler{Client: c, recorder: r}
}

// HandleNode works out what taints need to be applied to the node
func (h *Handler) HandleNode(instance *corev1.Node) (reconcile.Result, error) {

	// Get Pod for node
	pod, err := h.getDaemonsetPod(instance.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error fetching pods: %v", err)
	}

	copy := instance.DeepCopy()

	if pod == nil || podNotReady(pod) {
		if taintPresent(copy) {
			return reconcile.Result{}, nil
		}
		copy.Spec.Taints = addTaint(copy.Spec.Taints)
	} else {
		copy.Spec.Taints = removeTaint(copy.Spec.Taints)
	}

	if !reflect.DeepEqual(copy, instance) {
		instance = copy
		log.Printf("Updating Node %s\n", instance.Name)
		err = h.Update(context.TODO(), instance)
		// this is a hack to make the event work on a non-namespaced object
		copy.UID = types.UID(copy.Name)

		h.recorder.Eventf(copy, corev1.EventTypeNormal, "TaintsChanged", "Taints updated to %s", copy.Spec.Taints)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (h *Handler) getDaemonsetPod(nodeName string) (*corev1.Pod, error) {
	opts := client.InNamespace("kube-system")
	pods := &corev1.PodList{}
	err := h.List(context.TODO(), opts, pods)
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		for _, owner := range pod.OwnerReferences {
			if owner.Name == "kiam" {
				if pod.Spec.NodeName == nodeName {
					return &pod, nil
				}
			}
		}
	}

	return nil, nil
}

func podNotReady(pod *corev1.Pod) bool {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready == false {
			return true
		}
	}
	return false
}

func taintPresent(node *corev1.Node) bool {

	for _, taint := range node.Spec.Taints {
		if taint.Key == "kiam-not-ready" {
			return true
		}
	}
	return false
}

func addTaint(taints []corev1.Taint) []corev1.Taint {
	return append(taints, corev1.Taint{Key: "kiam-not-ready", Effect: corev1.TaintEffectNoSchedule})
}

func removeTaint(taints []corev1.Taint) []corev1.Taint {
	newTaints := []corev1.Taint{}

	for _, taint := range taints {
		if taint.Key == "kiam-not-ready" {
			continue
		}
		newTaints = append(newTaints, taint)
	}

	if len(newTaints) == 0 {
		return nil
	}

	return newTaints
}
