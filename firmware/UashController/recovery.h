#pragma once
#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include "deadline.h"

namespace uash {
constexpr uint32_t kRecordMagic = 0x55415348;
constexpr uint32_t kRecordVersion = 2;
constexpr int64_t kCheckpointIntervalUs = 5000000;

// One atomic NVS blob: identifiers, time and completion cannot be torn apart.
struct RecoveryRecord {
  uint32_t magic;
  uint32_t version;
  char session[37];
  char execution[37];
  uint64_t revision;
  uint64_t sequence;
  int64_t remainingMs;
  uint8_t completed;
  uint32_t checksum;
};

inline uint32_t recordChecksum(const RecoveryRecord& record) {
  const uint8_t* bytes = reinterpret_cast<const uint8_t*>(&record);
  uint32_t hash = 2166136261U;
  for (size_t i=0; i<offsetof(RecoveryRecord, checksum); ++i) hash = (hash ^ bytes[i])*16777619U;
  return hash;
}
inline bool uuidText(const char* text) {
  for (int i=0; i<36; ++i) {
    const char c=text[i];
    if (i==8 || i==13 || i==18 || i==23) { if (c!='-') return false; }
    else if (!((c>='0' && c<='9') || (c>='a' && c<='f'))) return false;
  }
  return text[36]=='\0';
}
inline bool validRecord(const RecoveryRecord& record) {
  return record.magic==kRecordMagic && record.version==kRecordVersion &&
    uuidText(record.session) && uuidText(record.execution) && record.revision>0 && record.sequence>0 &&
    record.remainingMs>=0 && record.remainingMs<=kMaxSessionMs && record.completed<=1 &&
    (!record.completed || record.remainingMs==0) && record.checksum==recordChecksum(record);
}
inline int64_t remainingAt(int64_t deadlineUs, int64_t nowUs) {
  return deadlineUs>nowUs ? (deadlineUs-nowUs)/1000 : 0;
}
// Clock skew cannot produce a checkpoint exceeding its authorized time budget.
inline int64_t boundedDeadline(int64_t candidate, int64_t receiptUs, int64_t remainingMs) {
  if (candidate<=receiptUs || remainingMs<=0 || remainingMs>kMaxSessionMs) return 0;
  const int64_t maximum=receiptUs+remainingMs*1000;
  return candidate<maximum?candidate:maximum;
}
}
