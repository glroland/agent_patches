import { Router } from 'express';
import * as agentsController from '../controllers/agentsController.js';

const router = Router();

router.get('/', agentsController.listAgents);
router.get('/:id', agentsController.getAgent);
router.get('/:id/activity', agentsController.getAgentActivity);
router.get('/:id/responsibilities', agentsController.getAgentResponsibilities);
router.get('/:id/memory', agentsController.getAgentMemory);
router.delete('/:id/memory', agentsController.clearAgentMemory);
router.post('/:id/messages', agentsController.sendAgentMessage);

export default router;
