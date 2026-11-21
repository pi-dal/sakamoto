"""Reproduce the legacy route editor's move-up operation on the synthetic fixture."""
import json
import subprocess


def format_legacy_config(content):
    formatted = subprocess.check_output(['sing-box', 'format', '-c', 'stdin'], input=content, text=True)
    assert any(rule.get('domain') == 'host-learned.fixture.example'
               for rule in json.loads(formatted)['route']['rules'])
    return formatted


def move_proxy_rule_up(content):
    config = json.loads(content)
    rules = config['route']['rules']
    index = next(i for i, rule in enumerate(rules) if rule.get('rule_set') == ['rs-proxy'])
    assert index > 0 and rules[index - 1].get('domain') == ['host-learned.fixture.example']
    rules.insert(index - 1, rules.pop(index))
    return json.dumps(config, separators=(',', ':'))
