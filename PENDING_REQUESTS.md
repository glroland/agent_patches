# Standard Model

# Fable

My LLM is being hammered by the agents and its having trouble keeping up.  Let's say each request is 2.5 minutes and it can only handle 2 concurrently.  Across 8 instances of endpoint-server, there is a log jam of as many as 50 pending requests.  Identify ways to streamline this.  You have full latitude to optimize the system but it could be compressing a prompt, limiting output tokens when you are confident the response should never be more than a certain amount, or reducing the number of times an LLM is called (by preexecuting a command of some type), etc.  System quality cannot be impacted but you are free to refactor the application to be more performant.
