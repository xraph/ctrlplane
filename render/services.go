package render

import (
	"fmt"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// renderServices templates the variable-bearing fields of each ServiceSpec
// (Image, Env values, Command, Args), returning new specs. Fields not
// templated (Ports, Volumes, etc.) are carried through unchanged.
func renderServices(in []provider.ServiceSpec, scope vars.Scope) ([]provider.ServiceSpec, error) {
	out := make([]provider.ServiceSpec, len(in))

	for i := range in {
		svc := in[i]

		image, err := tmplString(svc.Image, scope)
		if err != nil {
			return nil, fmt.Errorf("service %s image: %w", svc.Name, err)
		}

		svc.Image = image

		if len(svc.Env) > 0 {
			env := make(map[string]string, len(svc.Env))

			for k, v := range svc.Env {
				rendered, err := tmplString(v, scope)
				if err != nil {
					return nil, fmt.Errorf("service %s env %s: %w", svc.Name, k, err)
				}

				env[k] = rendered
			}

			svc.Env = env
		}

		command, err := tmplStrings(svc.Command, scope)
		if err != nil {
			return nil, fmt.Errorf("service %s command: %w", svc.Name, err)
		}

		svc.Command = command

		args, err := tmplStrings(svc.Args, scope)
		if err != nil {
			return nil, fmt.Errorf("service %s args: %w", svc.Name, err)
		}

		svc.Args = args
		out[i] = svc
	}

	return out, nil
}
