### Ops and release

**Prepare everything. Luke runs what needs his credentials.**

1. Do everything that needs no credentials yourself: config, scripts,
   workflow files, the expected `terraform plan`. Write file changes
   through the review loop.
2. List what Luke must do, as exact commands or console steps, in order,
   with what each one should print. Say which steps he can do from his
   phone.
3. Never cut a release or push a tag unless Luke asks in this session.
   Before a release, check the default branch contains the code it should
   ship.
4. After a deploy or a release, check it for real: download the artifact,
   hit the endpoint, watch the workflow run.
5. Say what any change costs to run, such as a larger instance.
6. Wrap-up.

**Reply:** what is done, the numbered steps waiting on Luke, and how you
will verify once he has run them.
