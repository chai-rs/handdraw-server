#!/usr/bin/env python3
"""Applied SQL is immutable; a pull request may append new complete increments."""
import re
import subprocess
import sys
from pathlib import Path

def git(*args):
    return subprocess.check_output(['git', *args])

base = sys.argv[1]
files = git('ls-tree', '-r', '--name-only', base, '--', 'migrations').decode().splitlines()
versions = []
for name in files:
    match = re.fullmatch(r'migrations/(\d{6})_[a-z0-9_]+\.(up|down)\.sql', name)
    if not match:
        continue
    versions.append(int(match[1]))
    current = Path(name)
    if not current.exists() or current.read_bytes() != git('show', f'{base}:{name}'):
        sys.exit(f'Applied migration changed: {name}. Append a new increment instead.')

maximum = max(versions, default=0)
added = {}
for path in Path('migrations').glob('*.sql'):
    if str(path) in files:
        continue
    match = re.fullmatch(r'(\d{6})_([a-z0-9_]+)\.(up|down)\.sql', path.name)
    if not match or int(match[1]) <= maximum:
        sys.exit(f'Invalid new migration order: {path}')
    added.setdefault((int(match[1]), match[2]), set()).add(match[3])
for key, directions in added.items():
    if directions != {'up', 'down'}:
        sys.exit(f'Missing migration direction: {key}')
new_versions = sorted(version for version, _ in added)
if new_versions != list(range(maximum + 1, maximum + 1 + len(new_versions))):
    sys.exit('New migration versions must be unique and sequential.')
