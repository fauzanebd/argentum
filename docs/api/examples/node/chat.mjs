import { Argentum } from '@argentum/sdk';

const client = new Argentum();

for await (const ev of client.chat.stream({
  message: 'What was total revenue in December 2024?',
  user_ref: 'quickstart',
})) {
  if (ev.event === 'delta') process.stdout.write(ev.data.content);
  if (ev.event === 'tool_call') process.stderr.write(`\n[${ev.data.tool}]\n`);
  if (ev.event === 'final') {
    console.log(`\n\nthread ${ev.data.thread_id}`);
    if (ev.data.usage) console.log(`cost $${ev.data.usage.cost_usd.toFixed(6)}`);
  }
}
