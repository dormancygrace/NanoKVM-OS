#!/usr/bin/env python3
"""Source inventory for the v3 Go replacement. No device access or code execution.

All registrations must resolve or generation fails; policy labels are reviewed
source annotations, not an executable replacement for authorization middleware.
"""
from pathlib import Path
import io, json, re, subprocess, tarfile
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'docs/experiments/v3.0'
BASELINE = 'a53b25579ab87cc98f85323b4743deb0d4da907b'

def baseline_sources():
    # Always inspect the exact comparison commit, not test helpers added later.
    names = subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', BASELINE], cwd=ROOT).decode().splitlines()
    names = [n for n in names if n.endswith('.go') or n.endswith('go.mod')]
    archive = subprocess.check_output(['git', 'archive', BASELINE, '--', *names], cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
        return {Path(m.name): tar.extractfile(m).read().decode() for m in tar.getmembers() if m.isfile()}

def generate():
    source_text = baseline_sources()
    files = sorted(p for p in source_text if p.parent.as_posix() == 'server/router' and p.suffix == '.go')
    files = [p for p in files if not p.name.endswith('_test.go')]
    constants = {}
    for p in files:
        for key, value in re.findall(r'\b(\w+)\s*=\s*("[^"\n]*")', source_text[p]):
            constants[key] = json.loads(value)
    def resolve(expr):
        parts = expr.strip().split('+')
        return ''.join(json.loads(x.strip()) if x.strip().startswith('"') else constants[x.strip()] for x in parts)
    routes = []
    for p in files:
        text = source_text[p]
        groups = {'r': ('', 'public')}
        for m in re.finditer(r'(\w+)\s*:=\s*(\w+)\.Group\(([^)]+)\)', text):
            var, parent, expr = m.groups()
            policy = ''
            tail = text[m.end():]
            if tail.startswith('.Use('):
                depth = 1
                pos = 5
                while depth:
                    depth += (tail[pos] == '(') - (tail[pos] == ')')
                    pos += 1
                policy = tail[:pos]
            if parent not in groups: raise ValueError((p, parent))
            prefix, inherited = groups[parent]
            auth = 'admin' if 'RoleAdmin' in policy else 'loopback-internal-token' if 'CheckLoopbackInternalToken' in policy else 'session' if 'CheckToken()' in policy else inherited
            groups[var] = (prefix + resolve(expr), auth)
        for m in re.finditer(r'\b(\w+)\.(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|Any)\(([^,\n]+),\s*([^\n]+)', text):
            var, method, expr, handler = m.groups()
            if var not in groups: raise ValueError((p, var))
            prefix, auth = groups[var]
            if 'RequireRole(authn.RoleAdmin)' in handler: auth = 'admin'
            elif 'CheckToken()' in handler: auth = 'session'
            if method == 'Any' and 'APIKeyMiddleware' in handler: auth = 'mcp-api-key'
            routes.append(dict(method=method.upper(), path=prefix+resolve(expr), authorization=auth,
                input_owner='requireInputOwner()' in handler,
                handler=handler.split(' //')[0].rstrip(')'),
                source=p.as_posix(), line=text[:m.start()].count('\n')+1,
                rust_status='pending', evidence=[]))
    keys = [(r['method'], r['path']) for r in routes]
    if len(set(keys)) != len(keys): raise ValueError('duplicate registration')
    source_files = [p for p in source_text if p.parts[0] == 'server' and p.suffix == '.go' and 'third_party' not in p.parts and not p.name.endswith('_test.go')]
    processes, paths, imports, mains = [], set(), set(), []
    for p in sorted(source_files):
        text = source_text[p]
        if re.search(r'^package main\b', text, re.M): mains.append(p.as_posix())
        for m in re.finditer(r'exec\.(Command(?:Context)?)\(([^\n]+)', text):
            processes.append(dict(source=p.as_posix(), line=text[:m.start()].count('\n')+1, expression=m.group(2)))
        paths.update(re.findall(r'"(/(?:etc|proc|sys|dev|run|tmp|usr|kvmapp|mnt|var)/[^"\n]*)"', text))
        imports.update(re.findall(r'"((?:github\.com|golang\.org|go\.|gopkg\.in)[^"\n]+)"',text))
    modules=[]
    for p in sorted(source_text):
        if p.name == 'go.mod':
            modules.append(dict(path=p.as_posix(), content=source_text[p]))
    return routes, dict(baseline=BASELINE,
        go_source_files=len(source_files), go_source_lines=sum(len(source_text[p].splitlines()) for p in source_files),
        main_package_files=mains, modules=modules, imported_dependencies=sorted(imports),
        process_invocation_sites=processes, absolute_path_literals=sorted(paths))

if __name__ == '__main__':
    routes, inventory = generate()
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT/'routes-baseline.json').write_text(json.dumps(routes, indent=2)+'\n')
    (OUT/'inventory.json').write_text(json.dumps(inventory, indent=2)+'\n')
    rows=['# Functional parity matrix', '', 'Generated source ledger; all entries begin pending. Hardware evidence is required for target claims.', '', '| Method | Path | Access | Go handler/source | Rust status | Evidence |', '|---|---|---|---|---|---|']
    for r in routes:
        rows.append(f"| {r['method']} | `{r['path']}` | {r['authorization']}"+(' + input owner' if r['input_owner'] else '')+f" | {r['source']}:{r['line']} | pending | — |")
    # Preserve verified migration statuses recorded after the baseline inventory.
    if not (OUT/'parity.md').exists():
        (OUT/'parity.md').write_text('\n'.join(rows)+'\n')
    print(json.dumps(dict(routes=len(routes), source_files=inventory['go_source_files'], source_lines=inventory['go_source_lines'], mains=inventory['main_package_files'], process_sites=len(inventory['process_invocation_sites']), paths=len(inventory['absolute_path_literals']))))
