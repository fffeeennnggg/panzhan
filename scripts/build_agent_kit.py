"""Build the public, credential-free Agent setup bundle embedded in PanZhan."""
from pathlib import Path
import zipfile

ROOT=Path(__file__).resolve().parents[1]
FILES=[
    'docs/agent-integration.md',
    'scripts/agent_event_relay.py',
    'integrations/agents/AGENT-INSTRUCTIONS.md',
    'integrations/agents/openclaw-mcp.example.json',
    'integrations/agents/hermes-mcp.example.yaml',
    'integrations/agents/hermes-webhook.example.yaml',
    'integrations/agents/routes.example.json',
]

def build():
    destination=ROOT/'internal/web/assets/agent-kit.zip'
    with zipfile.ZipFile(destination,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as bundle:
        for name in FILES:
            info=zipfile.ZipInfo(name,date_time=(2026,10,18,0,0,0))
            info.compress_type=zipfile.ZIP_DEFLATED
            info.external_attr=0o100644<<16
            bundle.writestr(info,(ROOT/name).read_bytes().replace(b'\r\n',b'\n'))
    print('Built credential-free Agent kit:',destination)
    return destination

if __name__=='__main__':build()
