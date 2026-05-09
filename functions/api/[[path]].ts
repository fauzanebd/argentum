const UPSTREAM = "https://argentum-api.gaia.smartsoft.co.id";

export const onRequest: PagesFunction = async ({ request }) => {
  const incoming = new URL(request.url);
  const target = `${UPSTREAM}${incoming.pathname}${incoming.search}`;

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
