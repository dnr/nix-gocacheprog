(final: prev: {
  # This is only required for Go < 1.24, it will be enabled by default after that.
  go_1_21 = prev.go_1_21.overrideAttrs { GOEXPERIMENT = "cacheprog"; doCheck = false; };
  go_1_22 = prev.go_1_22.overrideAttrs { GOEXPERIMENT = "cacheprog"; doCheck = false; };
  go_1_23 = prev.go_1_23.overrideAttrs { GOEXPERIMENT = "cacheprog"; doCheck = false; };

  nixGocacheprogHook = let const = import ./const.nix; in final.makeSetupHook {
    name = "nix-gocacheprog-hook";
  } (final.writeScript "nix-gocacheprog-hook.sh" ''
    _nixGocacheprogHook() {
      local client=${const.SandboxCacheDir}/client
      if [[ ! -x $client ]]; then
        echo "gocacheprog client not found, skipping GOCACHEPROG"
        return
      fi

      export GOCACHEPROG=$client
      echo "Setting GOCACHEPROG to $GOCACHEPROG"

      # default value from https://go.dev/ref/mod
      case "''${GOPROXY:=https://proxy.golang.org,direct}" in
        off|file*) # main build, do nothing
          ;;
        *) # module build
          $client -mode goproxy &
          export GOPROXY="http://localhost${const.ProxyListen},$GOPROXY"
          echo "Using nix-gocacheprog module proxy"
          echo "Setting GOPROXY to $GOPROXY"
          ;;
      esac
    }
    postConfigureHooks+=(_nixGocacheprogHook)
  '');

  # Add our hook to nativeBuildInputs.
  buildGoModule = args: prev.buildGoModule (
    args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });
  buildGo121Module = args: prev.buildGo121Module (
    args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });
  buildGo122Module = args: prev.buildGo122Module (
    args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });
  buildGo123Module = args: prev.buildGo123Module (
    args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });
  buildGo124Module = args: prev.buildGo124Module (
    args // { nativeBuildInputs = (args.nativeBuildInputs or []) ++ [ final.nixGocacheprogHook ]; });
})
