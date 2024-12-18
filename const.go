package main

var (
	SocketDir       = "<set in const.nix>"
	SandboxCacheDir = "<set in const.nix>"
)

const (
	SocketFile = "sock"

	PhaseBuild = "build"
	PhaseHook  = "hook"

	BuildIDPrefix = "bld-"
)
