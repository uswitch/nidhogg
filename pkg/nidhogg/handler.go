package nidhogg

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

// Handler performs the main business logic of the Wave controller
type Handler struct {
	client.Client
	recorder record.EventRecorder
	config   HandlerConfig
}

//HandlerConfig contains the options for Nidhogg
type HandlerConfig struct {
	Daemonsets   []Daemonset       `json:"daemonsets"`
	NodeSelector map[string]string `json:"nodeSelector"`
}

//Daemonset contains the name and namespace of a Daemonset
type Daemonset struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// NewHandler constructs a new instance of Handler
func NewHandler(c client.Client, r record.EventRecorder, conf HandlerConfig) *Handler {
	return &Handler{Client: c, recorder: r, config: conf}
}

// HandleNode works out what taints need to be applied to the node
func (h *Handler) HandleNode(instance *corev1.Node) (reconcile.Result, error) {

	log := logf.Log.WithName("nidhogg")

	//check whether node matches the nodeSelector
	selector := labels.SelectorFromSet(h.config.NodeSelector)
	if !selector.Matches(labels.Set(instance.Labels)) {
		return reconcile.Result{}, nil
	}

	copy := instance.DeepCopy()

	for _, daemonset := range h.config.Daemonsets {
		taintName := daemonset.Name + "-not-ready"
		// Get Pod for node
		pod, err := h.getDaemonsetPod(instance.Name, daemonset)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error fetching pods: %v", err)
		}

		if pod == nil || podNotReady(pod) {
			if !taintPresent(copy, taintName) {
				copy.Spec.Taints = addTaint(copy.Spec.Taints, taintName)
			}
		} else {
			copy.Spec.Taints = removeTaint(copy.Spec.Taints, taintName)
		}

	}

	if !reflect.DeepEqual(copy, instance) {
		instance = copy
		log.Info("Updating Node taints", "instance", instance.Name, "taints", instance.Spec.Taints)
		err := h.Update(context.TODO(), instance)
		// this is a hack to make the event work on a non-namespaced object
		copy.UID = types.UID(copy.Name)

		h.recorder.Eventf(copy, corev1.EventTypeNormal, "TaintsChanged", "Taints updated to %s", copy.Spec.Taints)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (h *Handler) getDaemonsetPod(nodeName string, ds Daemonset) (*corev1.Pod, error) {
	opts := client.InNamespace(ds.Namespace)
	pods := &corev1.PodList{}
	err := h.List(context.TODO(), opts, pods)
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		for _, owner := range pod.OwnerReferences {
			if owner.Name == ds.Name {
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

func taintPresent(node *corev1.Node, taintName string) bool {

	for _, taint := range node.Spec.Taints {
		if taint.Key == taintName {
			return true
		}
	}
	return false
}

func addTaint(taints []corev1.Taint, taintName string) []corev1.Taint {
	return append(taints, corev1.Taint{Key: taintName, Effect: corev1.TaintEffectNoSchedule})
}

func removeTaint(taints []corev1.Taint, taintName string) []corev1.Taint {
	newTaints := []corev1.Taint{}

	for _, taint := range taints {
		if taint.Key == taintName {
			continue
		}
		newTaints = append(newTaints, taint)
	}

	//empty array != nil
	if len(newTaints) == 0 {
		return nil
	}

	return newTaints
}
