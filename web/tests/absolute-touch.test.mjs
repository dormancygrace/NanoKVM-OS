import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';
import { MouseReportAbsolute } from '../src/lib/mouse.ts';
import { createTouchChord } from '../src/pages/desktop/mouse/touch-chord.ts';

function harness() {
  const events = new Map(), timers = new Map(), reports = [];
  const screen = { style: {}, parentElement: null,
    addEventListener: (name, fn) => events.set(name, fn),
    removeEventListener: name => events.delete(name),
    getBoundingClientRect: () => ({left:0,top:0,width:1000,height:1000}) };
  const atoms = { inputAdapterAtom:'auto', scrollDirectionAtom:1, scrollIntervalAtom:0,
    inputRegionAtom:null, resolutionAtom:{width:1000,height:1000} };
  let cleanup, time = 10000, timerID = 0;
  const exports = {};
  const modules = {
    react:{useRef:value=>({current:value}),useEffect:fn=>{cleanup=fn();}},
    'react/jsx-runtime':{jsx:()=>null},
    jotai:{useAtomValue:value=>value},
    '@/lib/input-adapter.ts':{resolveInputAdapter:()=> 'touchpad'},
    '@/lib/mouse.ts':{MouseReportAbsolute},
    '@/lib/websocket.ts':{client:{send:data=>reports.push([...data].slice(1))},MessageEvent:{Mouse:2}},
    '@/jotai/mouse.ts':atoms, '@/jotai/screen.ts':atoms,
    '../screen/geometry.ts':{
      getMediaSize:()=>({width:1000,height:1000}),
      getRenderedMediaRect:rect=>rect,
      fullFrameContent:()=>({left:0,top:0,width:1000,height:1000})
    },
    './touch-chord.ts':{createTouchChord}
  };
  const code = ts.transpileModule(fs.readFileSync(new URL('../src/pages/desktop/mouse/absolute.tsx',import.meta.url),'utf8'),
    {compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText;
  vm.runInNewContext(code, {
    exports, require:name=>{assert.ok(modules[name],name);return modules[name];},
    document:{getElementById:()=>screen},window:{addEventListener(){},removeEventListener(){}},
    navigator:{},Date:{now:()=>time},
    setTimeout:fn=>{timers.set(++timerID,fn);return timerID;},
    clearTimeout:id=>timers.delete(id),requestAnimationFrame:()=>1,cancelAnimationFrame(){}
  });
  exports.Absolute();
  return {
    reports,
    event(name,touches,changedTouches=[]){
      time+=50;
      events.get(name)({touches,changedTouches,preventDefault(){},stopPropagation(){}});
    },
    longPress(){for(const fn of timers.values()) fn(); timers.clear();},
    close(){cleanup();}
  };
}
const a={identifier:1,clientX:100,clientY:100};
const b={identifier:2,clientX:180,clientY:100};
const buttons=h=>h.reports.filter(r=>r[0]!==0).map(r=>r[0]);
test('chord emits right down/up at primary coordinates, never a left click',()=>{
  const h=harness();
  h.event('touchstart',[a]); h.event('touchstart',[a,b]);
  h.event('touchend',[a],[b]); h.event('touchend',[],[a]);
  assert.deepEqual(buttons(h),[2]);
  assert.equal(h.reports.at(-1)[0],0);
  assert.deepEqual(h.reports.at(-1).slice(1,5),h.reports[0].slice(1,5));
  h.close();
});
test('single and double tap retain one and two left clicks',()=>{
  const h=harness();
  for(let i=0;i<2;i++){h.event('touchstart',[a]);h.event('touchend',[],[a]);}
  assert.deepEqual(buttons(h),[1,1]);h.close();
});
test('scroll and cancellation do not generate clicks',()=>{
  const h=harness(), moved=[a,b].map(p=>({...p,clientY:p.clientY+30}));
  h.event('touchstart',[a]);h.event('touchstart',[a,b]);
  h.event('touchmove',moved);h.event('touchend',[],moved);
  assert.deepEqual(buttons(h),[]);assert.ok(h.reports.some(r=>r[5]!==0));
  h.event('touchstart',[a]);h.event('touchstart',[a,b]);h.event('touchcancel',[],[a,b]);
  assert.deepEqual(buttons(h),[]);h.close();
});
test('existing long press is released without a duplicate right click',()=>{
  const h=harness();h.event('touchstart',[a]);h.longPress();
  h.event('touchstart',[a,b]);h.event('touchend',[a],[b]);h.event('touchend',[],[a]);
  assert.deepEqual(buttons(h),[2]);assert.equal(h.reports.at(-1)[0],0);h.close();
});
test('double-tap drag still presses left, moves and releases',()=>{
  const h=harness(),moved={...a,clientX:140};
  h.event('touchstart',[a]);h.event('touchend',[],[a]);
  h.event('touchstart',[a]);h.event('touchmove',[moved]);h.event('touchend',[],[moved]);
  assert.deepEqual(buttons(h),[1,1,1]);assert.equal(h.reports.at(-1)[0],0);h.close();
});
