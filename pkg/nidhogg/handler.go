package nidhogg

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const (
	taintKey                 = "nidhogg.uswitch.com"
	taintOperationAdded      = "added"
	taintOperationRemoved    = "removed"
	annotationFirstTimeReady = taintKey + "/first-time-ready"
)

var (
	taintOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "taint_operations",
		Help: "Total number of added/removed taints operations",
	},
		[]string{
			"operation",
			"taint",
		},
	)
	taintOperationErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "taint_operation_errors",
		Help: "Total number of errors during taint operations",
	},
		[]string{
			"operation",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		taintOperations,
		taintOperationErrors,
	)
}

// Handler performs the main business logic of the Wave controller
type Handler struct {
	client.Client
	recorder record.EventRecorder
	config   HandlerConfig
}

// HandlerConfig contains the options for Nidhogg
type HandlerConfig struct {
	Daemonsets   []Daemonset `json:"daemonsets" yaml:"daemonsets"`
	NodeSelector []string    `json:"nodeSelector" yaml:"nodeSelector"`
	Selector     labels.Selector
}

func (hc *HandlerConfig) BuildSelectors() {
	print("test")
	hc.Selector = labels.Everything()
	for _, rawSelector := range hc.NodeSelector {
		if selector, err := labels.Parse(rawSelector); err != nil {
			panic(err)
		} else {
			requirements, _ := selector.Requirements()
			hc.Selector = hc.Selector.Add(requirements...)
		}
	}
}

// Daemonset contains the name and namespace of a Daemonset
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
	if !h.config.Selector.Matches(labels.Set(instance.Labels)) {
		return reconcile.Result{}, nil
	}

	nodeCopy, taintChanges, err := h.calculateTaints(instance)
	if err != nil {
		taintOperationErrors.WithLabelValues("calculateTaints").Inc()
		return reconcile.Result{}, fmt.Errorf("error caluclating taints for node: %v", err)
	}

	taintLess := true
	for _, taint := range nodeCopy.Spec.Taints {
		if strings.HasPrefix(taint.Key, taintKey) {
			taintLess = false
		}
	}

	var firstTimeReady string
	if taintLess {
		firstTimeReady = time.Now().Format("2006-01-02T15:04:05Z")
		if nodeCopy.Annotations == nil {
			nodeCopy.Annotations = map[string]string{
				annotationFirstTimeReady: firstTimeReady,
			}
		} else if _, ok := nodeCopy.Annotations[annotationFirstTimeReady]; !ok {
			nodeCopy.Annotations[annotationFirstTimeReady] = firstTimeReady
		} else {
			firstTimeReady = nodeCopy.Annotations[annotationFirstTimeReady]
		}
	} else if nodeCopy.Annotations != nil {
		firstTimeReady = nodeCopy.Annotations[annotationFirstTimeReady]
	}

	if !reflect.DeepEqual(nodeCopy, instance) {
		instance = nodeCopy
		log.Info("Updating Node taints", "instance", instance.Name, "taints added", taintChanges.taintsAdded, "taints removed", taintChanges.taintsRemoved, "taintLess", taintLess, "firstTimeReady", firstTimeReady)
		err := h.Update(context.TODO(), instance)
		if err != nil {
			taintOperationErrors.WithLabelValues("nodeUpdate").Inc()
			return reconcile.Result{}, err
		}
		for _, taintAdded := range taintChanges.taintsAdded {
			taintOperations.WithLabelValues(taintOperationAdded, taintAdded).Inc()
		}
		for _, taintRemoved := range taintChanges.taintsRemoved {
			taintOperations.WithLabelValues(taintOperationRemoved, taintRemoved).Inc()
		}

		// this is a hack to make the event work on a non-namespaced object
		nodeCopy.UID = types.UID(nodeCopy.Name)

		h.recorder.Eventf(nodeCopy, corev1.EventTypeNormal, "TaintsChanged", "Taints added: %s, Taints removed: %s, TaintLess: %v, FirstTimeReady: %q", taintChanges.taintsAdded, taintChanges.taintsRemoved, taintLess, firstTimeReady)
	}

	return reconcile.Result{}, nil
}

func (h *Handler) calculateTaints(instance *corev1.Node) (*corev1.Node, taintChanges, error) {

	nodeCopy := instance.DeepCopy()

	var changes taintChanges

	taintsToRemove := make(map[string]struct{})
	for _, taint := range nodeCopy.Spec.Taints {
		// we could have some older taints from a different configuration file
		// storing them all to reconcile from a previous state
		if strings.HasPrefix(taint.Key, taintKey) {
			taintsToRemove[taint.Key] = struct{}{}
		}
	}
	for _, daemonset := range h.config.Daemonsets {

		taint := fmt.Sprintf("%s/%s.%s", taintKey, daemonset.Namespace, daemonset.Name)
		// Get Pod for node
		pod, err := h.getDaemonsetPod(instance.Name, daemonset)
		if err != nil {
			return nil, taintChanges{}, fmt.Errorf("error fetching pods: %v", err)
		}

		if pod != nil && podReady(pod) {
			// if the taint is in the taintsToRemove map, it'll be removed
			continue
		}
		// pod doesn't exist or is not ready
		_, ok := taintsToRemove[taint]
		if ok {
			// we want to keep this already existing taint on it
			delete(taintsToRemove, taint)
			continue
		}
		// taint is not already present, adding it
		changes.taintsAdded = append(changes.taintsAdded, taint)
		nodeCopy.Spec.Taints = addTaint(nodeCopy.Spec.Taints, taint)
	}
	for taint := range taintsToRemove {
		nodeCopy.Spec.Taints = removeTaint(nodeCopy.Spec.Taints, taint)
		changes.taintsRemoved = append(changes.taintsRemoved, taint)
	}
	return nodeCopy, changes, nil
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

func podReady(pod *corev1.Pod) bool {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready == false {
			return false
		}
	}
	return true
}

func addTaint(taints []corev1.Taint, taintName string) []corev1.Taint {
	return append(taints, corev1.Taint{Key: taintName, Effect: corev1.TaintEffectNoSchedule})
}

func removeTaint(taints []corev1.Taint, taintName string) []corev1.Taint {
	var newTaints []corev1.Taint

	for _, taint := range taints {
		if taint.Key == taintName {
			continue
		}
		newTaints = append(newTaints, taint)
	}
	return newTaints
}
