# Nidhogg

Nidhogg is a controller that taints nodes based on whether a Pod from a specific Daemonset is running on them.

Sometimes you have a Daemonset that is so important that you don't want other pods to run on your node until that Daemonset is up and running on the node. Nidhogg solves this problem by tainting the node until your Daemonset pod is ready, preventing pods that don't tolerate the taint from scheduling there.

Nidhogg annotate the node when all the required taints are removed: `nidhogg.uswitch.com/first-time-ready: 2006-01-02T15:04:05Z`

Nidhogg was built using [Kubebuilder](https://github.com/kubernetes-sigs/kubebuilder)

## Usage

Nidhogg requires a yaml/json config file to tell it what Daemonsets to watch and what nodes to act on.
`nodeSelector` is a map of keys/values corresponding to node labels. `daemonsets` is an array of Daemonsets to watch, each containing two fields `name` and `namespace`. Nodes are tainted with taint that follows the format of `nidhogg.uswitch.com/namespace.name:NoSchedule`.

Example:

YAML:
```yaml
nodeSelector:
  node-role.kubernetes.io/node: ""
daemonsets:
  - name: kiam
    namespace: kube-system  
```
JSON:

```json
{
  "nodeSelector": {
    "node-role.kubernetes.io/node": ""
  },
  "daemonsets": [
    {
      "name": "kiam",
      "namespace": "kube-system"
    }
  ]
}
```
This example will taint any nodes that have the label `node-role.kubernetes.io/node=""` if they do not have a running and ready pod from the `kiam` daemonset in the `kube-system` namespace.
It will add a taint of `nidhogg.uswitch.com/kube-system.kiam:NoSchedule` until there is a ready kiam pod on the node.

If you want pods to be able to run on the nidhogg tainted nodes you can add a toleration:

```yaml
spec:
  tolerations:
  - key: nidhogg.uswitch.com/kube-system.kiam
    operator: "Exists"
    effect: NoSchedule
```

## Deploying
Docker images can be found at https://quay.io/uswitch/nidhogg

Example [Kustomize](https://github.com/kubernetes-sigs/kustomize) manifests can be found  [here](/config) to quickly deploy this to a cluster.

## Flags
```
-config-file string
    Path to config file (default "config.json")
-kubeconfig string
    Paths to a kubeconfig. Only required if out-of-cluster.
-leader-configmap string
    Name of configmap to use for leader election
-leader-election
    enable leader election
-leader-namespace string
    Namespace where leader configmap located
-master string
    The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.
-metrics-addr string
    The address the metric endpoint binds to. (default ":8080")
```
