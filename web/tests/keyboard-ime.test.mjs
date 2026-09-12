import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

function keyboardHarness() {
 const effects=[],sent=[],handlers=new Map(), windowHandlers=new Map();
 const refs=[];
 const doc={hidden:false,addEventListener:(k,v)=>handlers.set(k,v),removeEventListener:k=>handlers.delete(k)};
 const win={addEventListener:(k,v)=>windowHandlers.set(k,v),removeEventListener:k=>windowHandlers.delete(k)};
 const js=ts.transpileModule(readFileSync(new URL('../src/pages/desktop/keyboard/index.tsx',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,jsx:ts.JsxEmit.ReactJSX}}).outputText;
 const exports={};
 const state={active:false};
 const releaseCalls=[];
 class Report { keyDown(k){sent.push(['down',k]);return []} keyUp(k){sent.push(['up',k]);return []} reset(){sent.push(['reset']);return []} }
 const control={handleKeyDown:()=>false,handleKeyUp:k=>{releaseCalls.push(k);return false},reset(){}};
 new Function('exports','require','document','window',js)(exports,name=>{
  if(name==='react')return {useRef:v=>{const ref={current:v};refs.push(ref);return ref},useEffect:f=>effects.push(f)};
  if(name==='react/jsx-runtime')return {jsx(){},jsxs(){},Fragment(){}};
  if(name==='jotai')return {useAtomValue:atom=>atom==='enabled'?true:state};
  if(name.includes('browser'))return {getOperatingSystem:()=> 'Windows'};
  if(name==='@/lib/keyboard.ts')return {KeyboardReport:Report};
  if(name.includes('keymap'))return {isModifier:k=>k.startsWith('Meta')};
  if(name.includes('websocket'))return {client:{send(){}},MessageEvent:{Keyboard:1}};
  if(name==='@/jotai/keyboard.ts')return {isKeyboardEnableAtom:'enabled'};
  if(name.includes('picoclaw'))return {picoclawTakeoverStateAtom:'takeover'};
  if(name.includes('useLeaderKey'))return {useLeaderKey:()=>control};
  if(name.includes('useAltGr'))return {useAltGr:()=>control};
  if(name.includes('utils'))return {normalizeKeyCode:e=>e.code};
  if(name.includes('recorder'))return {Recorder(){}};
  throw new Error(name);
 },doc,win);
 exports.Keyboard();effects[0]();const cleanup=effects[1]();
 const emit=(name,code='',isComposing=false)=>handlers.get(name)({code,isComposing,timeStamp:100,preventDefault(){},stopPropagation(){}});
 return {emit,sent,cleanup,blur:()=>windowHandlers.get('blur')(),hidden:()=>{doc.hidden=true;emit('visibilitychange')},takeover:()=>{state.active=true;effects[0]()},releaseCalls};
}
test('IME releases tracked key but does not synthesize IME-only keys',()=>{
 const h=keyboardHarness();h.emit('keydown','KeyA');h.emit('compositionstart');h.emit('keyup','KeyA',true);
 assert.deepEqual(h.sent,[['down','KeyA'],['up','KeyA']]);
 assert.deepEqual(h.releaseCalls,['KeyA','KeyA']);
 h.emit('keydown','KeyB',true);h.emit('keyup','KeyB',true);
 assert.equal(h.sent.length,2);h.cleanup();
});
for(const release of ['blur','hidden','takeover'])test(`${release} resets composition even without compositionend`,()=>{
 const h=keyboardHarness();h.emit('keydown','AltRight');h.emit('compositionstart');h[release]();h.emit('keydown','KeyC');
 assert.deepEqual(h.sent.at(-1),['down','KeyC']);assert.ok(h.sent.some(e=>e[0]==='reset'));h.cleanup();
});
