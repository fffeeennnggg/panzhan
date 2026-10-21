"""Exercise a built PanZhan server with the official Python MCP client SDK.

Install mcp>=1,<2 in a test environment, or pass an isolated --sdk-path.
Uses temporary fixture credentials and does not contact a real cloud drive.
"""
from __future__ import annotations

import argparse
import asyncio
import http.cookiejar
import importlib.metadata
import json
import os
from pathlib import Path
import site
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT=Path(__file__).resolve().parents[1]

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path,required=True)
    parser.add_argument('--sdk-path',type=Path)
    args=parser.parse_args()
    if args.sdk_path:site.addsitedir(str(args.sdk_path.resolve()))
    import httpx
    from mcp import ClientSession
    from mcp.client.streamable_http import streamable_http_client
    with socket.socket() as listener:
        listener.bind(('127.0.0.1',0));port=listener.getsockname()[1]
    origin=f'http://127.0.0.1:{port}'
    cache=ROOT/'.cache';cache.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='mcp-sdk-smoke-',dir=cache) as temp:
        temp=Path(temp);data=temp/'data';data.mkdir();downloads=temp/'downloads';downloads.mkdir()
        (data/'bootstrap-password').write_text('sdk-fixture-pass',encoding='utf-8')
        process=subprocess.Popen([str(args.binary.resolve()),'-listen',f'127.0.0.1:{port}','-data',str(data),'-root',str(downloads)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=getattr(subprocess,'CREATE_NO_WINDOW',0))
        opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        def api(path,payload=None):
            raw=None if payload is None else json.dumps(payload).encode()
            request=urllib.request.Request(origin+'/api/'+path,data=raw,headers={'Content-Type':'application/json'})
            with opener.open(request,timeout=10) as response:return json.load(response)
        try:
            for _ in range(100):
                try:
                    status=api('status');break
                except OSError:time.sleep(.1)
            else:raise RuntimeError('Temporary fixture server did not start')
            assert status['version']=='0.2.2'
            api('login',{'password':'sdk-fixture-pass'})
            created=api('agents',{'name':'Official SDK fixture','scopes':['resources:read','downloads:create','tasks:read','events:read'],'expires_days':1})
            token=created['token']
            async def exercise():
                async with httpx.AsyncClient(headers={'Authorization':'Bearer '+token},timeout=20) as http:
                    async with streamable_http_client(origin+'/mcp',http_client=http,terminate_on_close=False) as (read,write,_):
                        async with ClientSession(read,write) as client:
                            initialized=await client.initialize()
                            tools=await client.list_tools()
                            names=sorted(tool.name for tool in tools.tools)
                            assert len(names)==7,names
                            tasks=await client.call_tool('panzhan_tasks',{})
                            assert not tasks.isError
                            assert tasks.structuredContent=={'tasks':[]}
                            events=await client.call_tool('panzhan_events',{'wait_seconds':0})
                            assert not events.isError and events.structuredContent=={'events':[]}
                            refused=await client.call_tool('panzhan_download',{'choice_id':'fixture','request_id':'sdk-message-1','user_confirmed':False})
                            assert refused.isError
                            return initialized.protocolVersion,names
            protocol,names=asyncio.run(exercise())
            api('agents/'+created['agent']['id'],{'action':'revoke'})
            print(json.dumps({'ok':True,'sdk':'mcp '+importlib.metadata.version('mcp'),'protocol':protocol,'tool_names':names,'scope':'isolated localhost fixture; no cloud or messaging calls'},ensure_ascii=False))
        finally:
            process.terminate()
            try:process.wait(timeout=10)
            except subprocess.TimeoutExpired:process.kill();process.wait(timeout=5)

if __name__=='__main__':main()
