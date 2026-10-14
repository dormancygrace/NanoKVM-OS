import assert from 'node:assert/strict';
import test from 'node:test';
import { openSerialTerminal, readSerialSession } from '../src/pages/terminal/launch.ts';
test('serial launch keeps parameters in isolated tab storage and removes opener', () => {
 const tabs=[];
 globalThis.window={open:()=>{const tab={sessionStorage:new Map(),location:{replace:url=>{tab.url=url}},opener:{},close:()=>{throw Error('unexpected close')}};tab.sessionStorage.setItem=tab.sessionStorage.set.bind(tab.sessionStorage);tabs.push(tab);return tab;}};
 openSerialTerminal('port=%2Fdev%2FttyGS0');openSerialTerminal('port=%2Fdev%2FttyS1&baud=9600');
 assert.equal(tabs[0].url,'/#terminal');assert.equal(tabs[0].opener,null);
 assert.equal(tabs[0].sessionStorage.get('nanokvm.serial-session'),'port=%2Fdev%2FttyGS0');
 assert.notEqual(tabs[0].sessionStorage.get('nanokvm.serial-session'),tabs[1].sessionStorage.get('nanokvm.serial-session'));
 delete globalThis.window;
});
test('reload reads serial settings from tab storage, not from the address', () => {
 const storage=new Map([['nanokvm.serial-session','port=%2Fdev%2FttyGS0&baud=9600']]);
 globalThis.window={location:{href:'https://kvm/#terminal?port=%2Fdev%2FttyS1'},sessionStorage:{getItem:k=>storage.get(k)}};
 assert.equal(readSerialSession().get('port'),'/dev/ttyGS0');
 storage.clear();assert.equal(readSerialSession().get('port'),null);
 delete globalThis.window;
});
