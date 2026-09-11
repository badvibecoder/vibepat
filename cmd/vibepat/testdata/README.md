# Test fixtures

## lspci.txt

Real `lspci -vv` output captured with root privileges from the project author's
development machine: 1462 lines, 36 PCI devices.

It is checked in because it is the only fixture that exercises the visual chunker
against the real shape of this format. Hand-written fixtures repeatedly hid bugs
that this file caught:

| Property | Why a synthetic fixture missed it |
| :--- | :--- |
| Only ~2.5% of lines are headers | Invented fixtures are header-dense, so a 10% detection threshold passed while misclassifying real output as indentation mode. |
| Indentation is a tab, not spaces | Fixtures written with spaces never exercised the tab path. |
| `LnkSta: Speed 2.5GT/s (downgraded), Width x4` | The real parenthetical sits *before* the width field; invented fixtures put it last. |
| 5 devices with a degraded PCIe link | The `link_downgrade` result was cross-checked against the machine's sysfs `current_link_speed` / `max_link_speed`, which agreed exactly. |

`cmd/vibepat/golden_test.go` asserts these properties directly. Any regeneration of this
file must preserve them, or those tests will fail loudly rather than pass
vacuously.

### What it does and does not contain

Contains: PCI vendor and device IDs, kernel driver names, link capability and
status, and bus topology. This is public hardware identification data.

Does **not** contain: IP addresses, MAC addresses, hostnames, usernames, file
paths, serial numbers, or machine identifiers. Verified by scanning for each.

The one thing it does reveal is the machine's hardware profile (an AMD platform
with a Radeon GPU and two Samsung NVMe drives). That is a deliberate choice: the
authenticity is what makes the fixture valuable, and it was already public in the
repository history.
