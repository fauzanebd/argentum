import sys

from argentum import Argentum

client = Argentum()

for ev in client.chat.stream("What was total revenue in December 2024?", user_ref="quickstart"):
    if ev.event == "delta":
        print(ev.data["content"], end="", flush=True)
    elif ev.event == "tool_call":
        print(f"\n[{ev.data.get('tool')}]", file=sys.stderr)
    elif ev.event == "final":
        print(f"\n\nthread {ev.data['thread_id']}")
        usage = ev.data.get("usage")
        if usage:
            print(f"cost ${usage['cost_usd']:.6f}")
