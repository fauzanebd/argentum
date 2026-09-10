import Widget from "./widget";

export default function Home() {
  return (
    <main>
      <h1>A Next.js page with a widget on it</h1>
      <p>
        This page is a server component. Only <code>./widget</code> is a client
        one, and only because the loader has nothing to do until there is a DOM.
      </p>
      <Widget />
    </main>
  );
}
