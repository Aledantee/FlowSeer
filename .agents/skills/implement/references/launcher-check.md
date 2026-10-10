# Check a launcher against the live runtime

Load when a unit changes a launcher that `drive` uses. Controlled replies
cannot prove that the launcher accepts the installed runtime's reply shape.

Start a disposable lane through the changed launcher and confirm that its
brief reaches the worker in the intended checkout. Grade and stop that lane
through the same launcher, then confirm that its terminal and worktree were
closed or kept as requested. Use the commands in `delegate`, Orca worker.
Keep cleanup limited to the disposable lane.

Record the runtime version, commands, observed replies, and cleanup result
in the unit's test evidence. Remove credentials from captured replies and
preserve the relevant reply shape as a regression fixture. An unavailable
runtime leaves the live check unverified and the unit blocked. A fixture
suite passing does not replace the live check.
