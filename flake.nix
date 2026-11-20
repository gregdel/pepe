{
  description = "pépé, a player for polochon";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      # Same format as the release tags: YYYYMMDD-HHMMSS.
      date = self.lastModifiedDate or "19700101000000";
      version = "${builtins.substring 0 8 date}-${builtins.substring 8 6 date}";
    in
    {
      packages = forAllSystems (pkgs: {
        default = pkgs.callPackage ./packaging/nix/package.nix { inherit version; };
      });
    };
}
