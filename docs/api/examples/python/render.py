import json

from argentum import Argentum

client = Argentum()  # reads ARGENTUM_API_KEY and ARGENTUM_BASE_URL

me = client.me()
print(f"key \"{me['key']['name']}\" on {me['company']['name']}, scopes: {', '.join(me['key']['scopes'])}")

with open("spec.json") as f:
    spec = json.load(f)

pdf = client.reports.render(spec)
with open("revenue-python.pdf", "wb") as f:
    f.write(pdf)

print(f"wrote revenue-python.pdf ({len(pdf)} bytes)")
