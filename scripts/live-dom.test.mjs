import test from 'node:test';
import assert from 'node:assert/strict';
import {stableResultOrder} from '../internal/web/assets/live-dom.mjs';

test('streaming higher-ranked results append without displacing a resource being read',()=>{
 const a={id:'a',title:'existing'},b={id:'b',title:'new higher quality'};
 const first=stableResultOrder([a]);
 const streamed=stableResultOrder([b,{...a,title:'updated metadata'}],first.ids);
 assert.deepEqual(streamed.ids,['a','b']);
 assert.equal(streamed.rows[0].title,'updated metadata');
 assert.equal(streamed.changed,true);
 assert.deepEqual(stableResultOrder([b,a]).ids,['b','a'],'explicit reorder applies current quality ranking');
});

test('invalid or filtered resources disappear while surviving resources keep their order',()=>{
 const result=stableResultOrder([{id:'new'},{id:'b'}],['a','b']);
 assert.deepEqual(result.ids,['b','new']);
 assert.deepEqual(stableResultOrder([],result.ids).rows,[]);
});
