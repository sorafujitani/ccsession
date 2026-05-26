{
  description = "ccsession - claude --resume frontend with fzf";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        goPkg = pkgs.go;
        ccsessionVersion = "0.1.0";
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [
            goPkg
            pkgs.gopls
            pkgs.gotools
            pkgs.go-tools
            pkgs.delve
            pkgs.goreleaser
            pkgs.fzf
            pkgs.ripgrep
          ];

          shellHook = ''
            # mise / asdf などから漏れた Go 環境変数を切り離し、
            # nix が用意した toolchain だけを使うようにする。
            unset GOROOT GOBIN GOTOOLDIR GOTOOLCHAIN
            export GOPATH="$HOME/go"
            export PATH="$GOPATH/bin:$PATH"
            echo "ccsession dev shell"
            ${goPkg}/bin/go version
          '';
        };

        packages.default = pkgs.buildGoModule {
          pname = "ccsession";
          version = ccsessionVersion;
          src = ./.;
          # 依存追加時はここを `lib.fakeHash` にしてビルドし、出力された
          # hash を貼り直す。現状は外部依存ゼロのため null で問題ない。
          vendorHash = null;
          subPackages = [ "cmd/ccsession" ];
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${ccsessionVersion}"
            "-X main.commit=nix"
          ];
          meta = {
            description = "claude --resume frontend with fzf";
            mainProgram = "ccsession";
            license = pkgs.lib.licenses.mit;
          };
        };

        apps.default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/ccsession";
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
