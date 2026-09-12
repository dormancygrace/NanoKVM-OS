import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
const source=readFileSync(new URL('../src/lib/mouse.ts',import.meta.url),'utf8');
const js=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
const exports={};new Function('exports',js)(exports);
test('relative pan preserves zero vertical wheel, signs and reset',()=>{
 const m=new exports.MouseReportRelative(); m.buttonDown(0);
 assert.deepEqual([...m.buildReport(0,0,0,-1)],[1,0,0,0,255]);
 assert.deepEqual([...m.buildReport(2,-3,1,2)],[1,2,253,1,2]);
 assert.deepEqual([...m.reset()],[0,0,0,0,0]);
});
test('absolute pan preserves coordinates, clamps and releases both wheels',()=>{
 const m=new exports.MouseReportAbsolute();m.buttonDown(2);
 assert.deepEqual([...m.buildReport(0x1234,0x5678,0,1)],[2,52,18,120,86,0,1]);
 assert.deepEqual([...m.buildReport(0,0,-500,500)],[2,0,0,0,0,129,127]);
 assert.deepEqual([...m.reset(0x1234,0x5678)],[0,52,18,120,86,0,0]);
});
