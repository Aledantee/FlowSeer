# Keep the proof when test setup changes

Load when the unit's rule makes a planned or standing test unbuildable.

A planned test moves to the first later, undispatched unit that can
construct its input. A unit worker reports the proposed move to its
coordinator. Before dispatching the recipient, the coordinator records
the move in both units' Tests lines, with the reason and the same
assertions. Until that unit passes, the proof remains outstanding.
Update the receiving unit's Files and After lines for the test and its
setup dependencies, and update any Waves list to match. Keep units that
share a test file in separate waves. A recipient already in flight or
passed cannot own the move through its old brief or pass. When no
undispatched unit can own it, stop for a revised placement.

A standing test whose planted state the rule removes gets setup that
survives the rule. Preserve its counts, assertions, and reasons, and add
a case proving what the new rule does to the original setup.

Stop when neither remedy preserves the proof, no later unit can own it,
or the remedy changes a Decision settled by the user. Report a permitted
move or setup change as a ruling, with the affected tests and units.
