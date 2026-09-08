// Best-effort diagnostic redaction, not a general-purpose secret scanner.
// Callers must register every generated credential and DSN in secrets.
export function summarizeGoFailure({ events = [], stderr = '', secrets = [] }) {
  const literals = [...new Set(secrets.filter((value) => typeof value === 'string' && value))]
    .sort((a, b) => b.length - a.length);
  function redact(value) {
    for (const secret of literals) value = value.replaceAll(secret, '[REDACTED]');
    return value
      .replace(/\b[a-z][a-z0-9+.-]*:\/\/[^\s/]+@[^\s"'<>]*/gi, '[REDACTED_URL]')
      .replace(/\b(Bearer|Basic)\s+[a-z0-9._~+\/=:-]+/gi, '$1 [REDACTED]')
      .replace(/\b((?:access[_-]?|refresh[_-]?|api[_-]?)?token|api[_-]?key|password|passwd|secret)\s*([=:])\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s&,;]+)/gi, '$1$2[REDACTED]');
  }
  function clean(value) {
    return redact(redact(String(value))
      .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
      .replace(/[\x00-\x09\x0b-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]/g, ''))
      .replaceAll('::', ': :');
  }
  const failures = events.filter((event) => event.Action === 'fail');
  const packages = new Set(failures.map((event) => event.Package));
  const tests = new Set(failures.filter((event) => event.Test).map((event) => JSON.stringify([event.Package, event.Test])));
  const lines = [];
  let bytes = 0;
  function append(value) {
    for (const line of clean(value).split('\n').filter((line) => line.trim())) {
      if (lines.length >= 50 || bytes >= 8192) return;
      const available = 8192 - bytes - (lines.length ? 1 : 0);
      let bounded = '', size = 0;
      for (const character of line) {
        const length = Buffer.byteLength(character);
        if (size + length > available) break;
        bounded += character; size += length;
      }
      if (!bounded) return;
      bytes += size + (lines.length ? 1 : 0);
      lines.push(bounded);
    }
  }
  for (const event of failures) append(`FAIL ${event.Package ?? '(unknown package)'}${event.Test ? ` / ${event.Test}` : ''}`);
  // Compiler output is often only on stderr, without a test fail event.
  append(stderr);
  for (const event of events) {
    if (event.Action !== 'output' || typeof event.Output !== 'string') continue;
    if (/^\s*(?:=== (?:RUN|PAUSE|CONT|NAME)|--- (?:PASS|SKIP))\b/.test(event.Output)) continue;
    const failedTest = tests.has(JSON.stringify([event.Package, event.Test]));
    const failedPackageDiagnostic = !event.Test && packages.has(event.Package)
      && /(?:\.go:\d+|panic:|fatal error:|\bFAIL\b|\[build failed\])/i.test(event.Output);
    if (failedTest || failedPackageDiagnostic) append(event.Output);
  }
  return lines;
}
