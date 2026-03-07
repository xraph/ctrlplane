package kubernetes

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// streamLogs opens a log stream from the first pod matching the instance selector.
func streamLogs(ctx context.Context, client kubernetes.Interface, namespace string, instanceID id.ID, opts provider.LogOptions) (io.ReadCloser, error) {
	selector := instanceSelector(instanceID)

	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
		Limit:         1,
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: list pods for logs: %w", err)
	}

	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("kubernetes: no pods found for instance %s", instanceID)
	}

	podName := pods.Items[0].Name

	logOpts := &corev1.PodLogOptions{
		Follow: opts.Follow,
	}

	if opts.Tail > 0 {
		tail := int64(opts.Tail)
		logOpts.TailLines = &tail
	}

	if !opts.Since.IsZero() {
		since := metav1.NewTime(opts.Since)
		logOpts.SinceTime = &since
	}

	stream, err := client.CoreV1().Pods(namespace).GetLogs(podName, logOpts).Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: stream logs: %w", err)
	}

	return stream, nil
}
