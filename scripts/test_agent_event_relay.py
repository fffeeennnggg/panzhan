import hashlib
import hmac
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('agent_event_relay',Path(__file__).with_name('agent_event_relay.py'))
relay_module=importlib.util.module_from_spec(spec);spec.loader.exec_module(relay_module)

class RelayTests(unittest.TestCase):
    def event(self):
        return {'id':7,'grant_id':'g','task_id':'job','title':'<external title>','status':'completed','context':'wechat:owner','directory':'media/job'}

    def test_hermes_hmac_routing_and_ack_after_delivery(self):
        with tempfile.TemporaryDirectory() as tmp:
            relay=relay_module.Relay('http://nas:9867','token','hermes','http://agent:8644/webhooks/panzhan','secret',{'wechat:owner':{'channel':'weixin','to':'configured-chat'}},Path(tmp)/'state.json')
            calls=[]
            def request(url,headers,payload=None,timeout=35):
                calls.append((url,headers,payload))
                if '/webhooks/' in url:
                    raw=json.dumps(payload,ensure_ascii=False,separators=(',',':')).encode()
                    self.assertEqual(headers['X-Hub-Signature-256'],'sha256='+hmac.new(b'secret',raw,hashlib.sha256).hexdigest())
                    self.assertEqual(payload['chat_id'],'configured-chat')
                    self.assertEqual(headers['X-GitHub-Delivery'],'panzhan-g-7')
                    return {'status':'delivered'}
                self.assertEqual(payload,{'event_id':7});return {'ok':True}
            with patch.object(relay_module,'request_json',side_effect=request):relay.deliver(self.event())
            self.assertEqual(len(calls),2);self.assertTrue(calls[1][0].endswith('/events/ack'))
            self.assertNotIn('token',(Path(tmp)/'state.json').read_text())

    def test_gateway_failure_is_not_acknowledged(self):
        with tempfile.TemporaryDirectory() as tmp:
            relay=relay_module.Relay('http://nas','token','hermes','http://agent/webhook','secret',{'wechat:owner':{'channel':'weixin','to':'chat'}},Path(tmp)/'state.json')
            with patch.object(relay_module,'request_json',return_value={'status':'ignored'}) as request:
                with self.assertRaises(ValueError):relay.deliver(self.event())
                self.assertEqual(request.call_count,1)
            self.assertFalse((Path(tmp)/'state.json').exists())

    def test_restart_after_acceptance_retries_ack_without_resending(self):
        with tempfile.TemporaryDirectory() as tmp:
            state=Path(tmp)/'state.json';routes={'wechat:owner':{'channel':'weixin','to':'chat'}}
            relay=relay_module.Relay('http://nas','token','openclaw','http://agent/hooks/agent','secret',routes,state)
            count=0
            def request(url,headers,payload=None,timeout=35):
                nonlocal count;count+=1
                if '/hooks/' in url:
                    self.assertEqual(payload['to'],'chat');self.assertTrue(payload['deliver']);return {'runId':'accepted'}
                raise OSError('lost ack')
            with patch.object(relay_module,'request_json',side_effect=request):
                with self.assertRaises(OSError):relay.deliver(self.event())
            self.assertEqual(count,2)
            restored=relay_module.Relay('http://nas','token','openclaw','http://agent/hooks/agent','secret',routes,state)
            with patch.object(relay_module,'request_json',return_value={'ok':True}) as request:
                restored.deliver(self.event());self.assertEqual(request.call_count,1);self.assertTrue(request.call_args.args[0].endswith('/events/ack'))

    def test_unknown_conversation_cannot_select_recipient(self):
        with tempfile.TemporaryDirectory() as tmp:
            relay=relay_module.Relay('http://nas','token','openclaw','http://agent/hooks/agent','secret',{},Path(tmp)/'state.json')
            with patch.object(relay_module,'request_json') as request:
                with self.assertRaises(ValueError):relay.deliver(self.event())
                request.assert_not_called()

if __name__=='__main__':unittest.main()
