#!/usr/bin/env python3
"""Source inventory for the v3 Go replacement. No device access or code execution.

All registrations must resolve or generation fails; policy labels are reviewed
source annotations, not an executable replacement for authorization middleware.
"""
from pathlib import Path
import json, re
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'docs/experiments/v3.0'

def generate():
    files = sorted((ROOT/'server/router').glob('*.go'))
    files = [p for p in files if not p.name.endswith('_test.go')]
    constants = {}
    for p in files:
        for key, value in re.findall(r'\b(\w+)\s*=\s*("[^"\n]*")', p.read_text()):
            constants[key] = json.loads(value)
    def resolve(expr):
        parts = expr.strip().split('+')
        return ''.join(json.loads(x.strip()) if x.strip().startswith('"') else constants[x.strip()] for x in parts)
    routes = []
    for p in files:
        text = p.read_text()
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
                source=str(p.relative_to(ROOT)), line=text[:m.start()].count('\n')+1,
                rust_status='pending', evidence=[]))
    keys = [(r['method'], r['path']) for r in routes]
    if len(set(keys)) != len(keys): raise ValueError('duplicate registration')
    source_files = [p for p in (ROOT/'server').rglob('*.go') if 'third_party' not in p.parts and not p.name.endswith('_test.go')]
    processes, paths, imports, mains = [], set(), set(), []
    for p in sorted(source_files):
        text = p.read_text()
        if re.search(r'^package main\b', text, re.M): mains.append(str(p.relative_to(ROOT)))
        for m in re.finditer(r'exec\.(Command(?:Context)?)\(([^\n]+)', text):
            processes.append(dict(source=str(p.relative_to(ROOT)), line=text[:m.start()].count('\n')+1, expression=m.group(2)))
        paths.update(re.findall(r'"(/(?:etc|proc|sys|dev|run|tmp|usr|kvmapp|mnt|var)/[^"\n]*)"', text))
        imports.update(re.findall(r'"((?:github\.com|golang\.org|go\.|gopkg\.in)[^"\n]+)"',text))
    modules=[]
    for p in sorted(ROOT.rglob('go.mod')):
        if '.git' not in p.parts:
            modules.append(dict(path=str(p.relative_to(ROOT)), content=p.read_text()))
    return routes, dict(baseline='a53b25579ab87cc98f85323b4743deb0d4da907b',
        go_source_files=len(source_files), go_source_lines=sum(len(p.read_text().splitlines()) for p in source_files),
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
    (OUT/'parity.md').write_text('\n'.join(rows)+'\n')
    print(json.dumps(dict(routes=len(routes), source_files=inventory['go_source_files'], source_lines=inventory['go_source_lines'], mains=inventory['main_package_files'], process_sites=len(inventory['process_invocation_sites']), paths=len(inventory['absolute_path_literals']))))
