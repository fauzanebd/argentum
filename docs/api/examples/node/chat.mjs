import { Argentum } from '@argentum/sdk';

const client = new Argentum();

// Which agent answers. The default is first in the list, so passing its id does
// the same thing as passing none — this is here to show where the id goes.
const [agent] = await client.agents();
if (agent) console.error(`asking ${agent.name}`);

for await (const ev of client.chat.stream({
  message: 'What was total revenue in December 2024?',
  user_ref: 'quickstart',
  ...(agent ? { agent_id: agent.id } : {}),
})) {
  if (ev.event === 'delta') process.stdout.write(ev.data.content);
  if (ev.event === 'tool_call') process.stderr.write(`\n[${ev.data.tool}]\n`);
  if (ev.event === 'final') {
    console.log(`\n\nthread ${ev.data.thread_id}`);
    if (ev.data.usage) console.log(`cost $${ev.data.usage.cost_usd.toFixed(6)}`);
  }
}
