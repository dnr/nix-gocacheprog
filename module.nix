{ config, pkgs, lib, ... }:
let
  pkg = import ./. { inherit pkgs; };
  const = import ./const.nix;
in
{
  nix.settings.pre-build-hook = "${pkg}/bin/hook";

  systemd.sockets.nix-gocacheprog = {
    description = "Server for Nix Go caching";
    wantedBy = [ "sockets.target" ];
    socketConfig.ListenStream = "${const.SocketDir}/sock";
  };

  systemd.services.nix-gocacheprog = {
    description = "Server for Nix Go caching";
    serviceConfig = {
      ExecStart = "${pkg}/bin/nix-gocacheprog -mode server";
      CacheDirectory = "nix-gocacheprog";
      DynamicUser = "true";
    };
  };
}
