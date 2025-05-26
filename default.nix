{ pkgs ? import <nixpkgs> {} }:
pkgs.buildGoModule {
  pname = "nix-gocacheprog";
  version = "0.0.2";
  src = pkgs.lib.cleanSource ./.;
  vendorHash = null;
  subPackages = [ "." ];
  postInstall = "ln -s nix-gocacheprog $out/bin/hook";
  env.CGO_ENABLED = "0";
  ldflags = pkgs.lib.mapAttrsToList (k: v: "-X main.${k}=${v}") (import ./const.nix);
}
