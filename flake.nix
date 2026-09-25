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
        default = pkgs.buildGoModule {
          pname = "gprt";
          inherit version;
          src = self;
          vendorHash = "sha256-C63G8TdWfi8G618Fzr9havgxqZp8bG0nNpXEUR0oHwQ=";
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
