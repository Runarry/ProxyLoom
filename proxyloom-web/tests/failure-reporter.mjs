// Emit bounded failure location immediately, even if a browser/server teardown is interrupted.
export default class FailureReporter {
  onTestEnd(test, result) {
    if (result.status !== test.expectedStatus) {
      for (const error of result.errors) process.stderr.write(`${test.title}\n${error.message ?? 'Test failed'}\n`)
    }
  }
}
