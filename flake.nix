{
  description = "A terminal UI for GitHub pull requests";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      # Flakes see no git tags, so the version is the commit itself.
      version = self.shortRev or self.dirtyShortRev or "dirty";
    in
    {
      packages = forAllSystems (pkgs: {
        # Not buildGoModule: nixpkgs' default go still lags behind go.mod.
        default = pkgs.buildGo127Module {
          pname = "gprt";
          inherit version;
          src = self;
          vendorHash = "sha256-I4s6yQONAlI8T2t74PPHvpYTQKaodvdGMn7DkzLYZ/Q=";
          # Not ./...: the darwin build sandbox forbids the loopback listeners
          # internal/gh's httptest servers need. CI runs the full suite.
          subPackages = [ "cmd/gprt" ];
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
          ];
          meta = {
            description = "A terminal UI for GitHub pull requests";
            homepage = "https://github.com/hirano00o/gprt";
            license = pkgs.lib.licenses.mit;
            mainProgram = "gprt";
          };
        };
      });
    };
}
