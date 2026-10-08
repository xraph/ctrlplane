package extension

import (
	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	cpcontract "github.com/xraph/ctrlplane/extension/contract"
)

// RegisterContractContributor publishes Ctrlplane's authorized dashboard intents.
func (e *Extension) RegisterContractContributor(d *dispatcher.Dispatcher, reg dash.Registry, wreg dash.WardenRegistry) error {
	return cpcontract.RegisterWithResolver(d, reg, wreg, cpcontract.Deps{ControlPlane: e.CtrlPlane, Ready: e.IsStarted})
}
