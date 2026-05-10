interface Env {
  UPSTREAM_URL: string;
}

export const onRequest: PagesFunction<Env> = async ({ request, env }) => {
  const upstream = env.UPSTREAM_URL;
  if (!upstream) {
    return new Response("UPSTREAM_URL not configured", { status: 500 });
  }
  const incoming = new URL(request.url);
  const target = `${upstream}${incoming.pathname}${incoming.search}`;

  const headers = new Headers(request.headers);
  headers.delete("host");
  headers.delete("cf-connecting-ip");
  headers.delete("cf-ipcountry");
  headers.delete("cf-ray");
  headers.delete("cf-visitor");
  headers.delete("x-forwarded-host");
  headers.delete("x-forwarded-proto");

  const init: RequestInit = {
    method: request.method,
    headers,
    redirect: "manual",
  };

  if (request.method !== "GET" && request.method !== "HEAD") {
    init.body = request.body;
    (init as { duplex?: string }).duplex = "half";
  }

  return fetch(target, init);
};
