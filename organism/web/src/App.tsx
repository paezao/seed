// The organism's user interface. A fresh Seed has no purpose yet, so this is
// all there is; the Seed replaces it as it grows.
export default function App() {
  return (
    <main className="empty">
      <img src="/logo.svg" alt="" width={56} height={56} />
      <h1>I don't have a purpose yet.</h1>
      <p>
        Tell me what to become at <a href="/_seed/">/_seed</a>.
      </p>
    </main>
  )
}
