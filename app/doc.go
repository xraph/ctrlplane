// Package app provides the CtrlPlane root orchestrator that wires all
// subsystems together. It is separated from the root ctrlplane package to
// avoid import cycles, since domain packages embed ctrlplane.Entity.
package app
