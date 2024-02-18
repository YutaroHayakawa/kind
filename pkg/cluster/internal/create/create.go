/*
Copyright 2019 The Kubernetes Authors.

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

package create

import (
	"context"
	"time"

	"sigs.k8s.io/kind/pkg/cluster/internal/delete"
	"sigs.k8s.io/kind/pkg/cluster/internal/providers"
	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
	"sigs.k8s.io/kind/pkg/internal/apis/config/encoding"
	"sigs.k8s.io/kind/pkg/internal/cli"
	"sigs.k8s.io/kind/pkg/log"

	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
	configaction "sigs.k8s.io/kind/pkg/cluster/internal/create/actions/config"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/displaysalutation"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/displayusage"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/exportkubeconfig"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/installcni"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/installstorage"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/kubeadminit"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/kubeadmjoin"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/loadbalancer"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/waitforready"
)

const (
	// Typical host name max limit is 64 characters (https://linux.die.net/man/2/sethostname)
	// We append -control-plane (14 characters) to the cluster name on the control plane container
	clusterNameMax = 50
)

// ClusterOptions holds cluster creation options
type ClusterOptions struct {
	Config       *config.Cluster
	NameOverride string // overrides config.Name
	// NodeImage overrides the nodes' images in Config if non-zero
	NodeImage      string
	Retain         bool
	WaitForReady   time.Duration
	KubeconfigPath string
	// see https://github.com/kubernetes-sigs/kind/issues/324
	StopBeforeSettingUpKubernetes bool // if false kind should setup kubernetes after creating nodes
	// Options to control output
	DisplayUsage      bool
	DisplaySalutation bool
	// Pause create operation after node provisioning
	PauseAfterNodeProvisioning bool
}

// errorCreatePaused implements ErrorCreatePaused interface
type errorCreatePaused struct {
	opts    *ClusterOptions
	ctx     *actions.ActionContext
	actions []actions.Action
}

func (e *errorCreatePaused) Error() string {
	return "create paused"
}

func (e *errorCreatePaused) Resume(ctx context.Context) error {
	return runActions(e.opts, e.ctx, e.actions)
}

// Cluster creates a cluster
func Cluster(logger log.Logger, p providers.Provider, opts *ClusterOptions) error {
	// validate provider first
	if err := validateProvider(p); err != nil {
		return err
	}

	// default / process options (namely config)
	if err := fixupOptions(opts); err != nil {
		return err
	}

	// Check if the cluster name already exists
	if err := alreadyExists(p, opts.Config.Name); err != nil {
		return err
	}

	// warn if cluster name might typically be too long
	if len(opts.Config.Name) > clusterNameMax {
		logger.Warnf("cluster name %q is probably too long, this might not work properly on some systems", opts.Config.Name)
	}

	// then validate
	if err := opts.Config.Validate(); err != nil {
		return err
	}

	// setup a status object to show progress to the user
	status := cli.StatusForLogger(logger)

	// we're going to start creating now, tell the user
	logger.V(0).Infof("Creating cluster %q ...\n", opts.Config.Name)

	// Create node containers implementing defined config Nodes
	if err := p.Provision(status, opts.Config); err != nil {
		// In case of errors nodes are deleted (except if retain is explicitly set)
		if !opts.Retain {
			_ = delete.Cluster(logger, p, opts.Config.Name, opts.KubeconfigPath)
		}
		return err
	}

	// TODO(bentheelder): make this controllable from the command line?
	actionsToRun := []actions.Action{
		loadbalancer.NewAction(), // setup external loadbalancer
		configaction.NewAction(), // setup kubeadm config
	}
	if !opts.StopBeforeSettingUpKubernetes && !opts.PauseAfterNodeProvisioning {
		// Add rest of the actions
		actionsToRun = append(actionsToRun, bottomHalfActions(opts)...)
	}

	// run all actions
	actionsContext := actions.NewActionContext(logger, status, p, opts.Config)
	if err := runActions(opts, actionsContext, actionsToRun); err != nil {
		return err
	}

	// if we are pausing, create continuation error
	if opts.PauseAfterNodeProvisioning {
		return &errorCreatePaused{
			opts:    opts,
			ctx:     actionsContext,
			actions: bottomHalfActions(opts),
		}
	}

	return nil
}

func runActions(opts *ClusterOptions, ctx *actions.ActionContext, actions []actions.Action) error {
	for _, action := range actions {
		if err := action.Execute(ctx); err != nil {
			if !opts.Retain {
				_ = delete.Cluster(ctx.Logger, ctx.Provider, ctx.Config.Name, opts.KubeconfigPath)
			}
			return err
		}
	}
	return nil
}

func bottomHalfActions(opts *ClusterOptions) []actions.Action {
	actionsToRun := []actions.Action{}
	actionsToRun = append(actionsToRun, kubernetesSetupActions(opts)...)
	// export kubeconfig
	actionsToRun = append(actionsToRun, exportkubeconfig.NewAction(opts.KubeconfigPath))
	// optionally display usage
	if opts.DisplayUsage {
		actionsToRun = append(actionsToRun, displayusage.NewAction(opts.KubeconfigPath))
	}
	// optionally give the user a friendly salutation
	if opts.DisplaySalutation {
		actionsToRun = append(actionsToRun, displaysalutation.NewAction())
	}
	return actionsToRun
}

func kubernetesSetupActions(opts *ClusterOptions) []actions.Action {
	actionsToRun := []actions.Action{}
	actionsToRun = append(actionsToRun,
		kubeadminit.NewAction(opts.Config), // run kubeadm init
	)
	// this step might be skipped, but is next after init
	if !opts.Config.Networking.DisableDefaultCNI {
		actionsToRun = append(actionsToRun,
			installcni.NewAction(), // install CNI
		)
	}
	// add remaining steps
	actionsToRun = append(actionsToRun,
		installstorage.NewAction(),                // install StorageClass
		kubeadmjoin.NewAction(),                   // run kubeadm join
		waitforready.NewAction(opts.WaitForReady), // wait for cluster readiness
	)
	return actionsToRun
}

// alreadyExists returns an error if the cluster name already exists
// or if we had an error checking
func alreadyExists(p providers.Provider, name string) error {
	n, err := p.ListNodes(name)
	if err != nil {
		return err
	}
	if len(n) != 0 {
		return errors.Errorf("node(s) already exist for a cluster with the name %q", name)
	}
	return nil
}

func fixupOptions(opts *ClusterOptions) error {
	// do post processing for options
	// first ensure we at least have a default cluster config
	if opts.Config == nil {
		cfg, err := encoding.Load("")
		if err != nil {
			return err
		}
		opts.Config = cfg
	}

	if opts.NameOverride != "" {
		opts.Config.Name = opts.NameOverride
	}

	// if NodeImage was set, override the image on all nodes
	if opts.NodeImage != "" {
		// Apply image override to all the Nodes defined in Config
		// TODO(fabrizio pandini): this should be reconsidered when implementing
		//     https://github.com/kubernetes-sigs/kind/issues/133
		for i := range opts.Config.Nodes {
			opts.Config.Nodes[i].Image = opts.NodeImage
		}
	}

	// default config fields (important for usage as a library, where the config
	// may be constructed in memory rather than from disk)
	config.SetDefaultsCluster(opts.Config)

	return nil
}

func validateProvider(p providers.Provider) error {
	info, err := p.Info()
	if err != nil {
		return err
	}
	if info.Rootless {
		if !info.Cgroup2 {
			return errors.New("running kind with rootless provider requires cgroup v2, see https://kind.sigs.k8s.io/docs/user/rootless/")
		}
		if !info.SupportsMemoryLimit || !info.SupportsPidsLimit || !info.SupportsCPUShares {
			return errors.New("running kind with rootless provider requires setting systemd property \"Delegate=yes\", see https://kind.sigs.k8s.io/docs/user/rootless/")
		}
	}
	return nil
}
