import Recruitment from "./Recruitment";

export default function App() {
  return (
    <>
      <header className="topbar">
        <div className="topbar-inner">
          <span className="brand">
            <span className="brand-mark" aria-hidden="true">R</span>
            Recruit
          </span>
          <span className="topbar-note">Hiring dashboard</span>
        </div>
      </header>
      <main>
        <section className="hero">
          <h1>Hiring, one place</h1>
          <p>
            Track open positions, applicants and their applications. Scoring jobs run on workers that start on demand, and the API wakes up
            from zero pods on the first request.
          </p>
        </section>
        <Recruitment />
      </main>
      <footer className="footer">Runs on Kubernetes with autoscaling API, workers and nodes.</footer>
    </>
  );
}
