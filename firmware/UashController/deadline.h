#pragma once
#include <stdint.h>
namespace uash {
constexpr int64_t kMaxSessionMs = 12LL * 60 * 60 * 1000;
// Convert the authorized UTC end into a monotonic deadline using a clock sample
// taken AFTER receiving HTTPS. Transport latency must not be subtracted twice.
inline int64_t deadlineForEnd(int64_t endMs, int64_t epochUs, int64_t monotonicUs) {
  if (epochUs <= 0 || monotonicUs < 0) return 0;
  const int64_t nowMs = epochUs / 1000;
  if (endMs <= nowMs || endMs > nowMs + kMaxSessionMs) return 0;
  return monotonicUs + endMs * 1000 - epochUs;
}
// A repeated authorization cannot move or restart its original deadline.
inline int64_t authorizedDeadline(int64_t current, int64_t candidate, bool newCommand) {
  return newCommand ? candidate : current;
}
}
