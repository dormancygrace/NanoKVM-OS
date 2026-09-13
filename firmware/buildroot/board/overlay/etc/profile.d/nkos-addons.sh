# Optional commands become available in new login/terminal sessions.
for nkos_dir in /opt/nkos/addons/*; do
    [ -f "$nkos_dir/addon.json" ] && [ -d "$nkos_dir/bin" ] || continue
    PATH="$PATH:$nkos_dir/bin"
done
export PATH
unset nkos_dir
