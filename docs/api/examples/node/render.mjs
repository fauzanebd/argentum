import { readFile, writeFile } from 'node:fs/promises';
import { Argentum } from '@argentum/sdk';

const client = new Argentum(); // reads ARGENTUM_API_KEY and ARGENTUM_BASE_URL

const me = await client.me();
console.log(`key "${me.key.name}" on ${me.company.name}, scopes: ${me.key.scopes.join(', ')}`);

const spec = JSON.parse(await readFile('spec.json', 'utf8'));
const pdf = await client.reports.render(spec);
await writeFile('revenue-node.pdf', pdf);

console.log(`wrote revenue-node.pdf (${pdf.length} bytes)`);
