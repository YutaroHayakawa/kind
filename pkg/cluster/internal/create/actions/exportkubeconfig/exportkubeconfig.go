package exportkubeconfig

import (
	"time"

	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
	"sigs.k8s.io/kind/pkg/cluster/internal/kubeconfig"
)

type Action struct {
	configPath string
}

// NewAction returns a new action for exporting kubeconfig
func NewAction(configPath string) actions.Action {
	return &Action{
		configPath: configPath,
	}
}

// Execute runs the action
func (a *Action) Execute(ctx *actions.ActionContext) (err error) {
	// try exporting kubeconfig with backoff for locking failures
	// TODO: factor out into a public errors API w/ backoff handling?
	// for now this is easier than coming up with a good API
	for _, b := range []time.Duration{0, time.Millisecond, time.Millisecond * 50, time.Millisecond * 100} {
		time.Sleep(b)
		if err = kubeconfig.Export(ctx.Provider, ctx.Config.Name, a.configPath, true); err == nil {
			break
		}
	}
	return
}
