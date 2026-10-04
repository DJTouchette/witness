#!/usr/bin/env python3
"""Verify literal source/reference evidence for every .NET command in an audit.

Usage: python3 scripts/verify-dotnet-audit.py audit-output.json
Read-only XML inspection, no SDK/build/restore. This independent check supports
literal references; imported/dynamic graph fixtures belong in planner tests and
the opt-in scratch SDK test. A target it cannot prove is an error, never skipped.
"""
import json
from pathlib import Path
import sys
import xml.etree.ElementTree as ET


def verify(rows):
    output = []
    for row in rows:
        plan = row.get('plan') or {}
        commands = [c for c in plan.get('commands', []) if c['argv'][0] == 'dotnet']
        if not commands:
            continue
        root = Path(plan['root']).resolve()

        def inside(p):
            resolved = p.resolve()
            resolved.relative_to(root)
            return resolved

        def project(p):
            return list(ET.parse(inside(p)).getroot().iter())

        def local(node):
            return node.tag.rsplit('}', 1)[-1]

        sources = [p for p in plan['selection']['changed_files'] if p.endswith('.cs')]
        owners = []
        for source in sources:
            d = inside(root / source).parent
            while True:
                manifests = list(d.glob('*.csproj'))
                if len(manifests) == 1:
                    owners.append(inside(manifests[0]))
                    break
                if len(manifests) > 1 or d == root:
                    raise ValueError(f'cannot prove unique source owner: {source}')
                d = d.parent

        targets = []
        for command in commands:
            if command['cwd'] != '.' or command['argv'][1] != 'test':
                raise ValueError(f'unexpected .NET command: {command}')
            target = inside(root / command['argv'][2])
            nodes = project(target)
            evidence = []
            for node in nodes:
                kind = local(node)
                if kind == 'IsTestProject' and (node.text or '').strip().lower() == 'true':
                    evidence.append(['IsTestProject', 'true'])
                if kind == 'PackageReference' and node.get('Include', '').lower() in {
                    'microsoft.net.test.sdk', 'xunit', 'nunit', 'mstest.testframework'
                }:
                    evidence.append(['PackageReference', node.get('Include')])
            if not evidence:
                raise ValueError(f'no literal test-project evidence: {target}')
            pending, seen, chain = [[target]], set(), None
            while pending:
                route = pending.pop(0)
                p = route[-1]
                if p in owners:
                    chain = [str(x.relative_to(root)) for x in route]
                    break
                if p in seen:
                    continue
                seen.add(p)
                for node in project(p):
                    if local(node) == 'ProjectReference' and node.get('Include'):
                        ref = node.get('Include').replace('\\', '/')
                        if any(c in ref for c in '$@%*?;'):
                            raise ValueError(f'cannot prove dynamic reference: {p}: {ref}')
                        pending.append(route + [inside(p.parent / ref)])
            if chain is None:
                raise ValueError(f'no literal source-reference chain: {target}')
            targets.append(dict(argv=command['argv'], cwd=command['cwd'],
                                test_evidence=evidence, reference_chain=chain))
        output.append(dict(case=row['name'], source_owners=[str(x.relative_to(root)) for x in owners],
                           verified_targets=targets))
    return output


if __name__ == '__main__':
    print(json.dumps(verify(json.loads(Path(sys.argv[1]).read_text())), indent=2))
