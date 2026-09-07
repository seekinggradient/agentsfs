// Execute the actual vendored browser engine, without network or generated audio.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const bundle = fs.readFileSync('internal/hub/assets/mdto/mdto.js', 'utf8');
const manifest = fs.readFileSync('internal/hub/assets/mdto/VERSION', 'utf8');
assert.equal(crypto.createHash('sha256').update(bundle).digest('hex'), manifest.match(/^sha256: (.+)$/m)[1]);
// The parser's browser entity decoder is unused by these plain-text fixtures.
const context = vm.createContext({TextEncoder, TextDecoder, URL,
  document: {createElement() {return {set innerHTML(value) {this.textContent = value;}};}}});
vm.runInContext(bundle, context, {timeout: 15000});
const engine = context.MDTO;
for (const method of ['parse', 'renderHtml', 'renderDiagnosticsHtml', 'renderBoard', 'renderWorkspace']) {
  assert.equal(typeof engine[method], 'function', method);
}
// An explicit minimum prevents an older bundle with fewer examples passing.
for (const name of ['todo', 'kanban', 'narrate', 'backlog', 'countdown', 'gantt', 'pdf', 'calendar', 'flashcards', 'timeline', 'podcast']) {
  assert.ok(engine.templates[name], `Missing supported format: ${name}`);
}
for (const [name, source] of Object.entries(engine.templates)) {
  const result = engine.parse(source);
  assert.equal(result.diagnostics.filter(d => d.severity === 'error').length, 0, `${name}: ${JSON.stringify(result.diagnostics)}`);
  const html = engine.renderHtml(result, {filename: `${name}.md`});
  assert.ok(html.includes('<html'), `${name}: empty render`);
  if (name === 'podcast') {
    assert.ok(result.podcast, 'Podcast must produce its native document');
    assert.ok(html.includes('podcast__turn'), 'Podcast dialogue view missing');
    assert.ok(html.includes('native two-speaker'), 'Podcast rendered as a generic report');
  }
}
const live = engine.renderBoard(engine.templates.todo, 'todo.md', 'embedded');
assert.ok(live.includes('mdto'), 'Live board bridge missing');
console.log(`Vendored renderer parses and renders all ${Object.keys(engine.templates).length} file formats.`);
