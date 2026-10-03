import assert from 'node:assert/strict';
import test from 'node:test';
import { parseUsbInput, availableMouseMode, noUsbInput } from '../src/lib/usb-input.ts';
import { resolveInputAdapter } from '../src/lib/input-adapter.ts';
test('disabled keyboard and relative mouse stay unavailable with Windows pointer',()=>{
 const s=parseUsbInput({keyboard:false,relative:false,absolute:true,pointerProfile:'windows'});
 assert.deepEqual(s,{keyboard:false,relative:false,absolute:true,available:true,pointerProfile:'windows'});
 assert.equal(availableMouseMode('relative',s),'absolute');
});
test('all eight compositions only select an enabled mouse',()=>{
 for(let mask=0;mask<8;mask++){
  const s=parseUsbInput({keyboard:!!(mask&1),relative:!!(mask&2),absolute:!!(mask&4)});
  assert.equal(s.available,mask!==0);
  for(const preferred of ['relative','absolute']){
   const mode=availableMouseMode(preferred,s);
   if(mode) assert.equal(s[mode],true);
   else assert.equal(s.relative||s.absolute,false);
  }
 }
});
test('missing/malformed status never enables input',()=>{
 for(const value of [null,{},'bad',{available:true},{keyboard:'true'}])
   assert.deepEqual(parseUsbInput(value),noUsbInput);
});
test('absolute touch cannot be disabled by a stale pointer-lock preference',()=>{
 const phone={hasTouch:true,coarsePointer:true,finePointer:false,canPointerLock:false};
 assert.equal(resolveInputAdapter('pointer-lock','absolute',phone),'touchpad');
 assert.equal(resolveInputAdapter('pointer-lock','relative',phone),'touchpad');
});
