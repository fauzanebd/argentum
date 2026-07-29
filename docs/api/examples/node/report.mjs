import { writeFile } from 'node:fs/promises';
import { Argentum } from '@argentum/sdk';

const client = new Argentum();

const job = await client.reports.create({
  prompt: 'Total revenue by month for 2024, with a bar chart.',
  format: 'pdf',
  user_ref: 'quickstart',
});
console.log(`report ${job.id} is ${job.status}`);

// Progress while the agent works. Skip this and call job.download() if all you
// want is the file — it polls on its own.
for await (const ev of job.stream()) {
  if (ev.event === 'progress') console.log(`  ${ev.data.type}${ev.data.tool ? ' ' + ev.data.tool : ''}`);
  if (ev.event === 'report') console.log(`  ${ev.data.status}`);
}

const pdf = await job.download();
await writeFile('agentic-node.pdf', pdf);
console.log(`wrote agentic-node.pdf (${pdf.length} bytes)`);
