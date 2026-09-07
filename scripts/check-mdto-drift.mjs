// Read-only check: upstream releases must trigger a reviewed re-vendor, never a runtime swap.
import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
const manifest = await readFile('internal/hub/assets/mdto/VERSION', 'utf8');
const pinned = manifest.match(/^sha256: (.+)$/m)[1];
const response = await fetch('https://markdownto.ai/app/mdto.js', {signal: AbortSignal.timeout(30000)});
if (!response.ok) throw new Error(`Cannot check published MarkdownTo renderer: HTTP ${response.status}`);
const published = createHash('sha256').update(Buffer.from(await response.arrayBuffer())).digest('hex');
if (published !== pinned) {
  throw new Error(`Hub renderer differs from the published MarkdownTo renderer (${pinned} vs ${published}). Re-vendor using docs/internals/markdownto-rendering.md, run compatibility tests, then deploy Hub.`);
}
console.log('Hub renderer matches the published MarkdownTo renderer.');
