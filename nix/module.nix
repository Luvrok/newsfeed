{ self }:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.dispatch;
  stateDir = "/var/lib/dispatch";

  defaults = {
    server = {
      addr = "127.0.0.1:${toString cfg.port}";
      web_dir = "${cfg.package}/share/dispatch/web";
      image_cache = "${stateDir}/images";
      allowed_origin = "";
    };
    database = {
      path = "${stateDir}/newsfeed.db";
      seed_file = "${cfg.package}/share/dispatch/seed.toml";
    };
    fetch = {
      timeout = "40s";
      workers = 8;
      max_items = 300;
      retention_days = 30;
      user_agent = "newsfeed/0.1 (+https://github.com/Luvrok/newsfeed)";
      proxy = "";
      allow_private_networks = false;
    };
  };

  settings = lib.recursiveUpdate defaults cfg.settings;
  configFile = (pkgs.formats.toml { }).generate "dispatch.toml" settings;
in
{
  options.services.dispatch = {
    enable = lib.mkEnableOption "Dispatch news collector";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.system}.default;
      description = "Dispatch package with the web assets.";
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 8180;
      description = "Local listen port. Put nginx or another proxy in front.";
    };

    settings = lib.mkOption {
      type = lib.types.attrs;
      default = { };
      description = ''
        Extra TOML settings. They are merged over the server, database and
        fetch defaults. Source instances live in the database and are managed
        from the web UI; an optional `sources` list here is only a first-run
        seed.
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = "Optional EnvironmentFile for NEWSFEED_PROXY, DISPATCH_WEB_DIR, etc.";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
    };
  };

  config = lib.mkIf cfg.enable {
    users.groups.dispatch = { };
    users.users.dispatch = {
      isSystemUser = true;
      group = "dispatch";
      home = stateDir;
      createHome = false;
    };

    systemd.services.dispatch = {
      description = "Dispatch news collector";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        User = "dispatch";
        Group = "dispatch";
        StateDirectory = "dispatch";
        StateDirectoryMode = "0750";
        WorkingDirectory = stateDir;
        ExecStart = "${cfg.package}/bin/newsd serve -config ${configFile}";
        Restart = "on-failure";
        RestartSec = 5;
        EnvironmentFile = lib.optional (cfg.environmentFile != null) cfg.environmentFile;
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = [ stateDir ];
        LimitNOFILE = 4096;
      };
    };

    networking.firewall.allowedTCPPorts = lib.mkIf cfg.openFirewall [ cfg.port ];
  };
}
