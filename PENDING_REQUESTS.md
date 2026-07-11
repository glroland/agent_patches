# Standard Model

## 1
The approvals tab in the central UI lists pending approval requests being surfaced from individual agents.  These requests are being flagged as high/medium/low risk.  However, the risk classification is being misused.  High Risk is being flagged for system updates from the vendor when high severity CVEs are being patched.  It seems like the LLM is flagged these as high risk as a result of having no other way of recognizing high importance.  Add a secondary tab for importance and evaluate importance vs risk independently within the agent.  Report both in the UI.  Like the Risk level bubble, show importance.  Have the Importance bubble show before the Risk bubble.

## 2
Let's say the memory-utilization-check runs every 5 minutes.  Is the LLM called everytime?  If not, under what circumstance is it called?

## 3
In a new or existing skill or loop or health check, look at system attributes such as temperature and track values over time.  If above normal levels, generate an alert.  Make sure the functionality is implemented for all OS Types.

# Fable

My LLM is being hammered by the agents and its having trouble keeping up.  Let's say each request is 2.5 minutes and it can only handle 2 concurrently.  Across 8 instances of endpoint-server, there is a log jam of as many as 50 pending requests.  Identify ways to streamline this.  You have full latitude to optimize the system but it could be compressing a prompt, limiting output tokens when you are confident the response should never be more than a certain amount, or reducing the number of times an LLM is called (by preexecuting a command of some type), etc.  System quality cannot be impacted but you are free to refactor the application to be more performant.
