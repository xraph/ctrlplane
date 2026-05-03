// Package nomad is a HashiCorp Nomad-backed provider.Provider.
//
// Each ctrlplane Instance maps to one Nomad Job named cp-<instanceID>
// containing a single TaskGroup with one Task per ServiceSpec. Init
// services use the prestart lifecycle hook; Sidecars use poststart
// with sidecar=true; Main services have no lifecycle stanza and run
// for the group's lifetime. TaskGroup network mode "bridge" gives
// siblings DNS resolution by service name within the group.
package nomad
