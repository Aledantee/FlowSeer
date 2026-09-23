# Promote a decision to a direction record

Load this when a decision in the plan passes the promotion test in
`SKILL.md`, step 1; a plan whose decisions all stay local to its units does
not need it.

Write `docs/architecture/<date>-<slug>-direction.md` with the frontmatter of
the existing records and `status: proposed-direction`, add a "Proposed
direction" row to `docs/architecture/README.md`, and cite the record from the
plan's Decisions. When it amends an accepted record, edit that record in the
unit that changes the code and name it in `amends`, and check its premises
against the tree as well as its paths, as `review`'s coordinator reading
does. The body gives the
context, the decision, the alternatives and why they lost, and the
consequences. Write only what the evidence supports; nobody edits a record
after acceptance. Only a person sets `accepted-direction`: ask for it in the
handoff and treat a proposed record as non-binding until then.
