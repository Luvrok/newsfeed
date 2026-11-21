{
  description = "Dispatch — a self-hosted news collector and reader";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      version = "0.2.0";
      systems = [ "x86_64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f (import nixpkgs { inherit system; }));
      clean = root: extra: nixpkgs.lib.cleanSourceWith {
        src = root;
        filter =
          path: type:
          let
            name = baseNameOf (toString path);
          in
          !(builtins.elem name ([ ".git" ] ++ extra));
      };
    in
    {
      packages = forAll (
        pkgs:
        let
          web = pkgs.buildNpmPackage {
            pname = "dispatch-web";
            inherit version;
            src = clean ./web [
              "node_modules"
              "dist"
              ".vite"
            ];
            npmDepsHash = "sha256-uR36f2qik0gV8jV0okrXVoLG9dnnx0d88528PDDerQk=";
            buildPhase = "npm run build";
            installPhase = ''
              mkdir -p $out/share/dispatch/web
              cp -r dist/. $out/share/dispatch/web/
            '';
          };

          dispatch = pkgs.buildGoModule {
            pname = "dispatch";
            inherit version;
            src = clean ./. [
              "node_modules"
              "dist"
              "data"
              "bin"
              "docs"
            ];
            vendorHash = "sha256-LywH6uhpch7uSWvJPs1TRaTA91vUlvxodpsZf3x9o0I=";
            subPackages = [ "cmd/newsd" ];
            env.CGO_ENABLED = "0";
            doCheck = true;
            checkPhase = "go test ./...";
            ldflags = [
              "-s"
              "-w"
              "-X"
              "main.version=${version}"
            ];
            postInstall = ''
              mkdir -p $out/share/dispatch
              cp -r ${web}/share/dispatch/web $out/share/dispatch/web
              install -Dm644 config/seed.toml $out/share/dispatch/seed.toml
            '';
          };
        in
        {
          default = dispatch;
          inherit dispatch web;
        }
      );

      nixosModules.default = import ./nix/module.nix { inherit self; };
      nixosModules.dispatch = self.nixosModules.default;

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            nodejs_22
            gopls
            gotools
            nixfmt
            curl
            jq
          ];
        };
      });

      formatter = forAll (pkgs: pkgs.nixfmt);
    };
}
