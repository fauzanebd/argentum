import sys

from argentum import Argentum

client = Argentum()

# Which agent answers. The default is first in the list, so passing its id does
# the same thing as passing none — this is here to show where the id goes.
agents = client.agents()
agent_id = agents[0]["id"] if agents else None
if agents:
    print(f"asking {agents[0]['name']}", file=sys.stderr)

for ev in client.chat.stream(
    "What was total revenue in December 2024?", user_ref="quickstart", agent_id=agent_id
):
    if ev.event == "delta":
        print(ev.data["content"], end="", flush=True)
    elif ev.event == "tool_call":
        print(f"\n[{ev.data.get('tool')}]", file=sys.stderr)
    elif ev.event == "final":
        print(f"\n\nthread {ev.data['thread_id']}")
        usage = ev.data.get("usage")
        if usage:
            print(f"cost ${usage['cost_usd']:.6f}")
