(final: prev: {
  # This is only required for Go < 1.24, it will be enabled by default after that.
  go_1_21_cacheprog = prev.go_1_21.overrideAttrs { GOEXPERIMENT = "cacheprog"; doCheck = false; };
  go_1_22_cacheprog = prev.go_1_22.overrideAttrs { GOEXPERIMENT = "cacheprog"; doCheck = false; };
  go_1_23_cacheprog = prev.go_1_23.overrideAttrs { GOEXPERIMENT = "cacheprog"; doCheck = false; };

  nixGocacheprogHook = let const = import ./const.nix; in final.makeSetupHook {
    name = "nix-gocacheprog-hook";
  } (final.writeScript "nix-gocacheprog-hook.sh" ''
    _nixGocacheprogHook() {
      if [[ -x ${const.SandboxCacheDir}/client ]]; then
        echo "Setting GOCACHEPROG"
        export GOCACHEPROG=${const.SandboxCacheDir}/client
      else
        echo "gocacheprog client not found, skipping GOCACHEPROG"
      fi
    }
    postConfigureHooks+=(_nixGocacheprogHook)
  '');

  # buildGo*Module is generated by a function that's not overridable, so we have to import it
  # again to override the "go" attribute. Then we override to put our hook in nativeBuildInputs.
  buildGo121ModuleCached = args: final.callPackage <nixpkgs/pkgs/build-support/go/module.nix> {
    go = final.go_1_21_cacheprog;
  } (args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });

  buildGo122ModuleCached = args: final.callPackage <nixpkgs/pkgs/build-support/go/module.nix> {
    go = final.go_1_22_cacheprog;
  } (args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });

  buildGo123ModuleCached = args: final.callPackage <nixpkgs/pkgs/build-support/go/module.nix> {
    go = final.go_1_23_cacheprog;
  } (args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });

  # 1.24 and above don't need a special package.
  buildGo124ModuleCached = args: prev.buildGo124Module (
    args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });
})
