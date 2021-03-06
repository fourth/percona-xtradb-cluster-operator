package perconaxtradbcluster

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-xtradb-cluster-operator/pkg/apis/pxc/v1alpha1"
	"github.com/percona/percona-xtradb-cluster-operator/pkg/pxc/app/statefulset"
)

func (r *ReconcilePerconaXtraDBCluster) updateStatus(cr *api.PerconaXtraDBCluster) (err error) {
	cr.Status = api.PerconaXtraDBClusterStatus{}

	cr.Status.PXC, err = r.appStatus(statefulset.NewNode(cr), cr.Spec.PXC, cr.Namespace)
	if err != nil {
		return fmt.Errorf("get pxc status: %v", err)
	}

	cr.Status.Host = cr.Name + "-" + "pxc"
	if cr.Status.PXC.Message != "" {
		cr.Status.Messages = append(cr.Status.Messages, "PXC: "+cr.Status.PXC.Message)
	}

	if cr.Spec.ProxySQL != nil && cr.Spec.ProxySQL.Enabled {
		cr.Status.ProxySQL, err = r.appStatus(statefulset.NewProxy(cr), cr.Spec.ProxySQL, cr.Namespace)
		if err != nil {
			return fmt.Errorf("get proxysql status: %v", err)
		}

		cr.Status.Host = cr.Name + "-" + "proxysql"
		if cr.Status.ProxySQL.Message != "" {
			cr.Status.Messages = append(cr.Status.Messages, "ProxySQL: "+cr.Status.ProxySQL.Message)
		}
	}

	switch {
	case cr.Status.PXC.Status == cr.Status.ProxySQL.Status:
		cr.Status.Status = cr.Status.PXC.Status
	case cr.Status.PXC.Status == api.AppStateError || cr.Status.ProxySQL.Status == api.AppStateError:
		cr.Status.Status = api.AppStateError
	case cr.Status.PXC.Status == api.AppStateInit || cr.Status.ProxySQL.Status == api.AppStateInit:
		cr.Status.Status = api.AppStateInit
	default:
		cr.Status.Status = api.AppStateUnknown
	}

	err = r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		// may be it's k8s v1.10 and erlier (e.g. oc3.9) that doesn't support status updates
		// so try to update whole CR
		err := r.client.Update(context.TODO(), cr)
		if err != nil {
			return fmt.Errorf("send update: %v", err)
		}
	}

	return nil
}

func (r *ReconcilePerconaXtraDBCluster) appStatus(app api.App, podSpec *api.PodSpec, namespace string) (api.AppStatus, error) {
	list := corev1.PodList{}
	err := r.client.List(context.TODO(),
		&client.ListOptions{
			Namespace:     namespace,
			LabelSelector: labels.SelectorFromSet(app.Labels()),
		},
		&list,
	)
	if err != nil {
		return api.AppStatus{}, fmt.Errorf("get list: %v", err)
	}

	status := api.AppStatus{
		Size:   podSpec.Size,
		Status: api.AppStateInit,
	}

	for _, pod := range list.Items {
		for _, cond := range pod.Status.Conditions {
			switch cond.Type {
			case corev1.ContainersReady:
				if cond.Status == corev1.ConditionTrue {
					status.Ready++
				} else if cond.Status == corev1.ConditionFalse {
					for _, cntr := range pod.Status.ContainerStatuses {
						if cntr.State.Waiting != nil && cntr.State.Waiting.Message != "" {
							status.Message += cntr.Name + ": " + cntr.State.Waiting.Message + "; "
						}
					}
				}
			case corev1.PodScheduled:
				if cond.Reason == corev1.PodReasonUnschedulable &&
					cond.LastTransitionTime.Time.Before(time.Now().Add(-1*time.Minute)) {
					status.Status = api.AppStateError
					status.Message = cond.Message
				}
			}
		}
	}

	if status.Size == status.Ready {
		status.Status = api.AppStateReady
	}

	return status, nil
}
