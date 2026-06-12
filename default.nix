{ stdenv, lib, buildGoModule, version, packageSrc ? ./. }:

buildGoModule rec {
  name = "gohookbridge-${version}";

  src = packageSrc;
  vendorHash = null;

  postUnpack = ''
    printf ${version} > $sourceRoot/gohookbridge/templates/version
  '';

  postInstall = ''
    # completions
    mkdir -p $out/share/bash-completion/completions/
    $out/bin/gohookbridge completion bash > $out/share/bash-completion/completions/gohookbridge
    mkdir -p $out/share/zsh/site-functions
    $out/bin/gohookbridge completion zsh > $out/share/zsh/site-functions/_gohookbridge
  '';

  meta = {
    description =
      "Command line server and client for webhooks deliveries (and https://smee.io)";
    homepage = "https://github.com/webcenter-fr/gohookbridge";
    license = lib.licenses.mit;
  };
}