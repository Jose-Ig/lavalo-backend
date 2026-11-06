#include "../UashController/recovery.h"
#include <assert.h>
#include <iostream>

int main() {
  uash::RecoveryRecord r{};
  r.magic=uash::kRecordMagic;r.version=uash::kRecordVersion;
  strcpy(r.session,"a7b9e2d0-1234-4567-89ab-0123456789ab");
  strcpy(r.execution,"b7b9e2d0-1234-4567-89ab-0123456789ab");
  r.revision=7;r.sequence=42;r.remainingMs=900000;
  r.checksum=uash::recordChecksum(r);
  assert(uash::validRecord(r));
  auto torn=r;torn.remainingMs=1200000;assert(!uash::validRecord(torn));
  auto invalid=r;invalid.completed=1;invalid.checksum=uash::recordChecksum(invalid);assert(!uash::validRecord(invalid));
  r.completed=1;r.remainingMs=0;r.sequence++;r.checksum=uash::recordChecksum(r);assert(uash::validRecord(r));
  assert(uash::remainingAt(10000000,4000000)==6000);
  assert(uash::remainingAt(0,4000000)==0);
  assert(uash::remainingAt(10000000,11000000)==0);
  assert(uash::boundedDeadline(12000000,1000000,10000)==11000000);
  assert(uash::boundedDeadline(10500000,1000000,10000)==10500000);
  assert(uash::boundedDeadline(0,1000000,10000)==0);
  std::cout << "PASS: checkpoint integrity, offline completion and monotonic remaining time\n";
}
