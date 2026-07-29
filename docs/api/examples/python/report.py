from argentum import Argentum

client = Argentum()

job = client.reports.create(
    "Total revenue by month for 2024, with a bar chart.",
    format="pdf",
    user_ref="quickstart",
)
print(f"report {job.id} is {job.status}")

# Progress while the agent works. Skip this and call job.download() if all you
# want is the file — it polls on its own.
for ev in job.stream():
    if ev.event == "progress":
        print("  " + ev.data["type"] + (" " + ev.data["tool"] if ev.data.get("tool") else ""))
    if ev.event == "report":
        print("  " + ev.data["status"])

pdf = job.download()
with open("agentic-python.pdf", "wb") as f:
    f.write(pdf)

print(f"wrote agentic-python.pdf ({len(pdf)} bytes)")
