{
  description = "Outband — browser BMC for IPMI, AMT, and friends";

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
          pname = "outband";
          version =
            if self ? shortRev then
              "0.1.0-alpha.2-${self.shortRev}"
            else
              "0.1.0-alpha.2-dirty";

          src = self;

          # Pure-Go stack (modernc.org/sqlite); keep CGO off like the Dockerfile.
          env.CGO_ENABLED = "0";

          # GOFLAGS=-mod=vendor breaks `go mod vendor` in the modules FOD (imports
          # unresolved → incomplete vendor). Clear it for the vendor phase only.
          # Must set env.GOFLAGS (not top-level GOFLAGS) — newer nixpkgs puts GOFLAGS in env.
          overrideModAttrs = oldAttrs: {
            env = (oldAttrs.env or { }) // {
              GOFLAGS = "";
            };
          };

          vendorHash = "sha256-+EQ+gE/8Vf91BSDCKnWUrcD//+0P/d/PLKIJqDDZYTM=";

          subPackages = [ "cmd/outband" ];

          ldflags = [
            "-s"
            "-w"
          ];

          meta = with pkgs.lib; {
            description = "Browser BMC UI (IPMI, AMT, …)";
            homepage = "https://github.com/TheMinecraftGuyGuru/outband";
            license = licenses.mit;
            mainProgram = "outband";
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
            pkgs.nodejs_22
          ];
        };
      }
    );
}
