package displayusage

import (
	"fmt"

	"github.com/alessio/shellescape"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
	"sigs.k8s.io/kind/pkg/cluster/internal/kubeconfig"
)

type Action struct {
	configPath string
}

// NewAction returns a new action for displaying usage
func NewAction(configPath string) *Action {
	return &Action{
		configPath: configPath,
	}
}

// Execute runs the action
func (a *Action) Execute(ctx *actions.ActionContext) error {
	// construct a sample command for interacting with the cluster
	kctx := kubeconfig.ContextForCluster(ctx.Config.Name)
	sampleCommand := fmt.Sprintf("kubectl cluster-info --context %s", kctx)
	if a.configPath != "" {
		// explicit path, include this
		sampleCommand += " --kubeconfig " + shellescape.Quote(a.configPath)
	}
	ctx.Logger.V(0).Infof(`Set kubectl context to "%s"`, kctx)
	ctx.Logger.V(0).Infof("You can now use your cluster with:\n\n" + sampleCommand)
	return nil
}
