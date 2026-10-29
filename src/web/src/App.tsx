import Recruitment from "./Recruitment";

export default function App() {
  return (
    <main>
      <h1>Recruitment on a scaling cluster</h1>
      <p className="muted">
        A small applicant tracking app. The API scales to zero pods when idle and wakes up on the first request; scoring jobs run on
        workers that scale with the queue.
      </p>
      <Recruitment />
    </main>
  );
}
