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

const taintKey = "nidhogg.uswitch.com"

// Handler performs the main business logic of the Wave controller
type Handler struct {
	client.Client
	recorder record.EventRecorder
	config   HandlerConfig
}

//HandlerConfig contains the options for Nidhogg
type HandlerConfig struct {
	Daemonsets   []Daemonset       `json:"daemonsets" yaml:"daemonsets"`
	NodeSelector map[string]string `json:"nodeSelector" yaml:"nodeSelector"`
}

//Daemonset contains the name and namespace of a Daemonset
type Daemonset struct {
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
}

type taintChanges struct {
	taintsAdded   []string
	taintsRemoved []string
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

	copy, taintChanges, err := h.caclulateTaints(instance)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error caluclating taints for node: %v", err)
	}

	if !reflect.DeepEqual(copy, instance) {
		instance = copy
		log.Info("Updating Node taints", "instance", instance.Name, "taints added", taintChanges.taintsAdded, "taints removed", taintChanges.taintsRemoved)
		err := h.Update(context.TODO(), instance)
		if err != nil {
			return reconcile.Result{}, err
		}

		// this is a hack to make the event work on a non-namespaced object
		copy.UID = types.UID(copy.Name)

		h.recorder.Eventf(copy, corev1.EventTypeNormal, "TaintsChanged", "Taints added: %s, Taints removed: %s", taintChanges.taintsAdded, taintChanges.taintsRemoved)
	}

	return reconcile.Result{}, nil
}

func (h *Handler) caclulateTaints(instance *corev1.Node) (*corev1.Node, taintChanges, error) {

	copy := instance.DeepCopy()

	var changes taintChanges

	for _, daemonset := range h.config.Daemonsets {

		taint := fmt.Sprintf("%s.%s", daemonset.Namespace, daemonset.Name)
		// Get Pod for node
		pod, err := h.getDaemonsetPod(instance.Name, daemonset)
		if err != nil {
			return nil, taintChanges{}, fmt.Errorf("error fetching pods: %v", err)
		}

		if pod == nil || podNotReady(pod) {
			if !taintPresent(copy, taint) {
				copy.Spec.Taints = addTaint(copy.Spec.Taints, taint)
				changes.taintsAdded = append(changes.taintsAdded, taint)
			}
		} else if taintPresent(copy, taint) {
			copy.Spec.Taints = removeTaint(copy.Spec.Taints, taint)
			changes.taintsRemoved = append(changes.taintsRemoved, taint)
		}

	}
	return copy, changes, nil
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

func taintPresent(node *corev1.Node, taintValue string) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Key == taintKey && taint.Value == taintValue {
			return true
		}
	}
	return false
}

func addTaint(taints []corev1.Taint, taintValue string) []corev1.Taint {
	return append(taints, corev1.Taint{
		Key:    taintKey,
		Value:  taintValue,
		Effect: corev1.TaintEffectNoSchedule,
	})
}

func removeTaint(taints []corev1.Taint, taintValue string) []corev1.Taint {
	newTaints := []corev1.Taint{}

	for _, taint := range taints {
		if taint.Key == taintKey && taint.Value == taintValue {
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
