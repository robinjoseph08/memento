# Store access decisions by scope

Memento will store Album, Moment, and Album Entry Access Decisions in separate tables rather than using a polymorphic scope column or materializing effective access. Album decisions allow access, while Moment and Album Entry decisions may allow or deny it. Each table can enforce normal foreign keys and one decision per Person and target. The Publishing module will resolve the most specific available decision at read time, then fall back through broader scopes before denying access.
