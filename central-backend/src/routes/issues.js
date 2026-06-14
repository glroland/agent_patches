import { Router } from 'express';
import * as issuesController from '../controllers/issuesController.js';

const router = Router();

router.get('/', issuesController.listIssues);

export default router;
