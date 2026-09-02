export function App() {
  return (
    <main className="grid min-h-screen place-items-center bg-slate-100 p-8 text-slate-900">
      <section
        aria-labelledby="app-title"
        className="w-full max-w-2xl rounded-3xl border border-slate-200 bg-white p-8 shadow-xl sm:p-16"
      >
        <p className="mb-4 text-xs font-bold tracking-[0.16em] text-blue-700 uppercase">
          Memento
        </p>
        <h1
          className="text-[clamp(2.5rem,8vw,5rem)] leading-[0.95] font-semibold tracking-[-0.04em]"
          id="app-title"
        >
          Your application starts here.
        </h1>
        <p className="mt-7 max-w-xl text-lg leading-7 text-slate-600">
          React, Go, PostgreSQL, and the development tools needed to ship them
          together.
        </p>
      </section>
    </main>
  );
}
