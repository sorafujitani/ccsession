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
          # go.mod の依存（TOML パーサ + pure-Go SQLite ドライバ）を取得・
          # 検証する固定出力ハッシュ。依存を更新したら一旦 pkgs.lib.fakeHash
          # に戻してビルドし、報告された hash を貼り直す。
          vendorHash = "sha256-yBf0ScxjkCRb6Cp/QnGpR3O/llTPqc4vIQSMZ5hoP/o=";
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
