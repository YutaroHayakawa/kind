package displaysalutation

import (
	"math/rand"
	"time"

	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
)

type Action struct {
}

// NewAction returns a new action for displaying salutation
func NewAction() *Action {
	return &Action{}
}

// Execute runs the action
func (a *Action) Execute(ctx *actions.ActionContext) error {
	ctx.Logger.V(0).Info("")
	salutations := []string{
		"Have a nice day! 👋",
		"Thanks for using kind! 😊",
		"Not sure what to do next? 😅  Check out https://kind.sigs.k8s.io/docs/user/quick-start/",
		"Have a question, bug, or feature request? Let us know! https://kind.sigs.k8s.io/#community 🙂",
	}
	r := rand.New(rand.NewSource(time.Now().UTC().UnixNano()))
	s := salutations[r.Intn(len(salutations))]
	ctx.Logger.V(0).Info(s)
	return nil
}
