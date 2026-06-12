# System services

Somne system integrations, make sure to edit the downloaded file and adjust the target URL before applying it directly on your system/cluster

## macOS

```shell
mkdir -p $HOME/Library/LaunchAgents/
curl -L https://raw.githubusercontent.com/webcenter-fr/gohookbridge/main/misc/com.webcenter-fr.gohookbridge.plist -o $HOME/Library/LaunchAgents/com.webcenter-fr.gohookbridge.plist
$EDITOR $HOME/Library/LaunchAgents/com.webcenter-fr.gohookbridge.plist

launchctl load -w ~/Library/LaunchAgents/com.webcenter-fr.gohookbridge.plist
```

## Linux / Systemd

```shell
mkdir -p $HOME/.config/systemd/user
curl -L https://raw.githubusercontent.com/webcenter-fr/gohookbridge/main/misc/gohookbridge.service -o $HOME/.config/systemd/user/gohookbridge.service
$EDITOR  $HOME/.config/systemd/user/gohookbridge.service
systemctl --user enable --now gohookbridge
```