#include "../UashController/deadline.h"
#include <cassert>
#include <iostream>
int main() {
  const int64_t epoch = 1793808000000000LL;
  const int64_t start = 2000000;
  const int64_t endMs = epoch / 1000 + 60000;
  const int64_t expected = start + 60000000;
  // The same end is honored for immediate responses or 5/10-second HTTPS waits.
  for (int64_t latencyUs : {0LL, 5000000LL, 10000000LL}) {
    assert(uash::deadlineForEnd(endMs, epoch + latencyUs, start + latencyUs) == expected);
  }
  // Preserve sub-second accuracy; truncating wall time would shift the cutoff.
  assert(uash::deadlineForEnd(endMs, epoch + 5250123, start + 5250123) == expected);
  // No cumulative shrink or extension as subsequent replies or NTP samples vary.
  int64_t deadline = uash::authorizedDeadline(0, expected, true);
  for (int i = 0; i < 1000; ++i) {
    int64_t varied = expected + ((i % 2) ? 5000000 : -5000000);
    deadline = uash::authorizedDeadline(deadline, varied, false);
    assert(deadline == expected);
  }
  // A cutoff/deactivated command never restarts through a repeated reply.
  assert(uash::authorizedDeadline(0, expected, false) == 0);
  // A new paid authorization may extend the end, even if received slowly.
  int64_t extension = uash::deadlineForEnd(endMs + 900000, epoch + 15000000, start + 15000000);
  assert(uash::authorizedDeadline(deadline, extension, true) == expected + 900000000);
  // No internet is needed to compare the fixed monotonic deadline.
  assert(expected - 1 < deadline);
  assert(!(expected < deadline));
  assert(uash::deadlineForEnd(endMs, endMs * 1000, expected) == 0);
  assert(uash::deadlineForEnd(endMs, (endMs + 1) * 1000, expected + 1000) == 0);
  assert(uash::deadlineForEnd(epoch / 1000 + uash::kMaxSessionMs + 1, epoch, start) == 0);
  assert(uash::deadlineForEnd(endMs, 0, start) == 0);
  std::cout << "PASS: HTTPS latency, fractional seconds, duplicate replies, paid extension and offline cutoff\n";
}
