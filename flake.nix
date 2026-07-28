{
  description = "mIPMI — Go + HTMX BMC UI over IPMI 2.0 / RMCP+";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        # Module requires Go 1.25+; pin so default go drift cannot break the build.
        buildGo = pkgs.buildGoModule.override { go = pkgs.go_1_25; };
      in
      {
        packages.default = buildGo {
          pname = "mipmi";
          version =
            if self ? shortRev then
              "0.1.0-alpha.2-${self.shortRev}"
            else
              "0.1.0-alpha.2-dirty";

          src = self;

          # Pure-Go stack (modernc.org/sqlite); keep CGO off like the Dockerfile.
          env.CGO_ENABLED = "0";

          vendorHash = "sha256-z7zOGV+kpdZTQmJNktTWjIMSjvxKshiu+Joel0SsIqA=";

          subPackages = [ "cmd/mipmi" ];

          ldflags = [
            "-s"
            "-w"
          ];

          meta = with pkgs.lib; {
            description = "BMC management UI over IPMI 2.0 / RMCP+";
            homepage = "https://github.com/TheMinecraftGuyGuru/mipmi";
            license = licenses.mit;
            mainProgram = "mipmi";
            platforms = platforms.unix;
          };
        };

        apps.default = flake-utils.lib.mkApp {
          drv = self.packages.${system}.default;
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go_1_25
            pkgs.git
          ];
        };
      }
    );
}
