---
title: Limits
description: Torrent Drive size, memory, CPU, speed, and peer limits, and what they mean for large transfers.
---

# Limits

## Fixed limits

- One active torrent, up to 256 GiB and 4,096 files.
- One piece up to 64 MiB, and an 8 MiB upload buffer.
- Worker: 768 MiB RAM, no swap, two CPUs, and 128 processes or threads.
- Dashboard: 128 MiB RAM, no swap, and half of one CPU.
- Torrent download and Drive upload: 16 MiB/s each.
- At most 32 connected peers and four connection attempts at a time.
- At most 16 retained jobs and 128 MiB of job metadata.
- No seeding, WebTorrent, webseeds, or symlink torrents.

## What to expect

RAM use does not depend on the total file size. This makes files larger than
your free disk possible. It does not make the transfer fast, and it does not
find peers that are not there.

- Pieces are requested in order, which can be slower than a normal torrent client.
- The worker does not seed. Check private tracker rules before you use it.
- A missing piece cannot be skipped.
- A long interruption can expire a Drive upload session. That file then restarts
  from zero. Finished files stay intact.
