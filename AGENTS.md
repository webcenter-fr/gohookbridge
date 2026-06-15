# Implement new feature

Do this when implement a new feature:
- follow the contributing.md rules and best practice
- implement feature on back (golang)
- implement feature on front (VUE)
- back and front must work together
- prefer use library when available. Check the ranking before introduce it and validate with user
- maintain documentation and contributing up to date
- implement all tests to cover the feature
  - include backend unit test
  - include front unit test
  - integration test to valide UI and backend work together.
  - Integration test for backend (api, publisher, consumer (the client))
- Ask to user before plan or implement if not clear
- Ask user if you think there are better way, better pattern or better framework to implement the feature 