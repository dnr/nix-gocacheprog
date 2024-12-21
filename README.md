# nix-gocacheprog

**Use Go build and module caching inside Nix builds**

Go build and module caching is great, but it doesn't work in Nix builds because
of the sandbox.

`nix-gocacheprog` pokes a hole in the sandbox to make it work. Iterating on Go
builds in Nix is fast again!

Note: Only do this if you trust that Go's build cache is accurate (this
seems pretty well-accepted). Maybe don't do it on your release builds.

This is just single-machine caching, nothing over the network yet.

`nix-gocacheprog` *also* caches module downloads, by routing them through the
build cache with a module proxy.
This isn't as big of a win as caching builds, since `buildGoModule` and
`gomod2nix` already cache modules by using a separate derivation, but it does
help if you're using `buildGoModule` with `vendorHash` and add a single
dependency: all the others won't be downloaded again.

Only for NixOS for now, but it shouldn't be that hard to make it work on other
systems that Nix runs on (contributions welcome).


## Usage

1. Import [module.nix](module.nix) in your NixOS config.
1. Add [overlay.nix](overlay.nix) to your NixOS config or to your project.
1. - For Go 1.21 through 1.23:
     - Use `buildGo122ModuleCached` instead of `buildGo122Module`
   - For Go 1.24+, _either_:
     - Use `buildGo124ModuleCached` instead of `buildGo124Module`
     - Add `nixGocacheprogHook` to your `nativeBuildInputs`.

### Example

(You'll probably want to use flakes or niv or npins or whatever, but this should
give you the idea.)

**`configuration.nix`**
```nix
{
  imports = [
    "${fetchTarball "https://github.com/dnr/nix-gocacheprog/archive/main.tar.gz"}/module.nix"
  ];
}
```

**`myproject/default.nix`**
```nix
{ pkgs ? import <nixpkgs> { config = {}; overlays = [
  (import "${fetchTarball "https://github.com/dnr/nix-gocacheprog/archive/main.tar.gz"}/overlay.nix")
]; } }:
let
  # This lets it work even if the attribute is missing:
  buildGoModule = pkgs.buildGo123ModuleCached or pkgs.buildGo123Module;
in buildGoModule {
  name = "myproject-1.2.3";
  vendorHash = "...";
  src = pkgs.lib.cleanSource ./.;
}
```

## How does it work?

### GOCACHEPROG

`GOCACHEPROG` is an experimental Go feature that will be promoted to stable with Go 1.24.
It lets you plug in an external program to handle the Go build cache.
There's a "reference implemention" of the protocol here: https://github.com/bradfitz/go-tool-cache

### Sandbox and pre-build-hook

We can plug in a program but how do we share the cache across builds?
We poke a hole using Nix's `pre-build-hook`:
Nix runs a program of our choice before setting up the sandbox, and it can add
specific paths to the sandbox (with bind mounts).

So we just add the whole Go build cache, right?

Well, no. That would work but it would be a big mess, with multiple uids writing
to one directory, that could read each other's files…

We add:
- Our `GOCACHEPROG` binary.
- A unix socket, for our `GOCACHEPROG` to communicate over.
- A build-specific directory to put the cached files in (this is why it needs to
  be a pre-build-hook and not just extra-sandbox-paths).

The other end of the socket is a daemon that runs on the host, that speaks the
`GOCACHEPROG` protocol with extensions for setting up build-specific
directories. The cache itself is shared among all builds on the system, but each
build can only see its own files (files that it knows the input hash of).

### Module and overlay

The module:

- Sets `nix.settings.pre-build-hook`.
- Sets up the daemon, including socket activation.
- Adds a nixpkgs overlay to expose the modified builders.
  Your project may not include system overlays, so you may have to include it explicitly there.

The overlay sets up:

- Variants of `go_1_21`, `go_1_22`, and `go_1_23` built with `GOEXPERIMENT=cacheprog`.
- `nixGocacheprogHook` that you can add to `nativeBuildInputs`.
- `buildGo121ModuleCached` (and `122` and `123`) that work like `buildGo121Module` but:
  - Use the gocacheprog-enabled Go build.
  - Automatically add `nixGocacheprogHook` to `nativeBuildInputs`.

`nixGocacheprogHook` does:

- Checks if it looks like the pre-build-hook has run. If not, do nothing.
- Set `GOCACHEPROG` to our binary (in client mode).

### Module cache

The above sets up caching for builds. What about the module cache?

The module cache uses a separate mechanism and protocol.
To intercept it, we can use a module proxy.
The proxy mostly passes things through to an upstream proxy
(`https://proxy.golang.org` by default, but it respects `GOPROXY`),
but for `.mod` and `.zip` files (the immutable ones),
it uses the build cache.

This works in the "module derivation" of `buildGoModule`, which is a FOD.
The "main derivation" sets `GOPROXY=off` so this doesn't apply.

To support this, `nixGocacheprogHook` also does:

- If `GOPROXY` is `off` or `file:...`, do nothing.
- Runs the binary in goproxy mode in the background, listening on a localhost port.
- Sets `GOPROXY` to point to that proxy.


## TODO

- Cache size limit
- Access-based TTL
- Flake (contributions welcome)
- Extend over the network
- Make it work with `sandbox = false` (see [this issue](https://github.com/NixOS/nix/issues/2985))
- Make it work with non-NixOS systems
- Make it work with `gomod2nix`?


## Comparisons

### https://github.com/numtide/build-go-cache

`build-go-cache` takes a different approach: it pre-builds dependencies and puts
them in a separate derivation that is a dependency of your build.

Some differences:

`build-go-cache`
- is pure
- only caches dependency builds
- requires an extra step to list external packages and an extra file in the repo
- works anywhere

`nix-gocacheprog`
- should provide more speedup
- caches build results of the main project also
- caches everything including test runs
- caches module downloads too
- after Go 1.24, will cache binary linking results also
- requires system-level changes (`pre-build-hook`)
- is technically impure, but we generally trust the Go build tool
- is only set up for NixOS right now, but should work elsewhere with a little effort


### https://github.com/adisbladis/gobuild.nix

`gobuild.nix` also uses `GOCACHEPROG` but in a purer way, with separate
derivations per module that contain subsets of the cache:

`gobuild.nix`
- is pure
- overhauls the Nix Go build system

`nix-gocacheprog`
- is a drop-in replacement for `buildGoModule`
- requires system-level changes (`pre-build-hook`)
- is technically impure, but we generally trust the Go build tool
- is only set up for NixOS right now, but should work elsewhere with a little effort


## LICENSE

Parts of this project were forked from https://github.com/bradfitz/go-tool-cache
and so it inherits its 3-clause BSD license.
